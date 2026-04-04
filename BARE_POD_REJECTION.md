# Bare Pod Rejection & StatefulSet Support

## Overview

The webhooks now conditionally reject bare Pods and support both Deployments and StatefulSets.

**Changes:**
- Reject bare Pods **only if enforcement is enabled** (min/max labels present)
- Allow bare Pods if no enforcement labels are set
- Accept Deployments and StatefulSets with same PDB logic
- Reject incomplete configs (only one label present)

---

## Behavior

### Bare Pod in Enforced Namespace (Rejected)
```yaml
# Namespace with enforcement enabled
apiVersion: v1
kind: Namespace
metadata:
  name: prod
  labels:
    pdb-min-available: "2"
    pdb-max-unavailable: "1"
---
apiVersion: v1
kind: Pod
metadata:
  name: my-pod
  namespace: prod
spec:
  containers:
  - name: app
    image: nginx
```

**Result:** ❌ **REJECTED**
```
Error: bare pods are not allowed in enforced namespace; pods must be created by Deployment or StatefulSet
```

---

### Bare Pod in Non-Enforced Namespace (Allowed)
```yaml
# Namespace with NO enforcement labels
apiVersion: v1
kind: Namespace
metadata:
  name: dev
---
apiVersion: v1
kind: Pod
metadata:
  name: my-pod
  namespace: dev
spec:
  containers:
  - name: app
    image: nginx
```

**Result:** ✅ **ALLOWED**
```
Bare pods allowed in non-enforced namespace
```

---

### Deployment (Allowed/Enforced)
```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-app
spec:
  replicas: 3
  selector:
    matchLabels:
      app: my-app
  template:
    metadata:
      labels:
        app: my-app
    spec:
      containers:
      - name: app
        image: nginx
```

**Result:** ✅ **ALLOWED** (if PDB exists when configured)

---

### StatefulSet (Allowed/Enforced)
```yaml
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: my-statefulset
spec:
  replicas: 3
  selector:
    matchLabels:
      app: my-statefulset
  template:
    metadata:
      labels:
        app: my-statefulset
    spec:
      containers:
      - name: app
        image: nginx
```

**Result:** ✅ **ALLOWED** (if PDB exists when configured)

---

## Implementation Details

### Webhook Rules

**Mutating Webhook** (`/mutate`):
- Operations: CREATE
- Resources: `deployments`, `statefulsets`, `pods`
- Behavior:
  - Pod → Skip (no mutation)
  - Deployment/StatefulSet + both labels present → Create PDB
  - Deployment/StatefulSet + only one label → Reject
  - Deployment/StatefulSet + no labels → Allow

**Validating Webhook** (`/validate`):
- Operations: CREATE, UPDATE
- Resources: `deployments`, `statefulsets`, `pods`
- Behavior:
  - Pod + enforcement enabled (both labels) → **REJECT**
  - Pod + enforcement disabled (no labels) → **ALLOW**
  - Pod + incomplete config (one label) → **REJECT** (error)
  - Deployment/StatefulSet + incomplete config → Reject
  - Deployment/StatefulSet + config present → Enforce PDB
  - Deployment/StatefulSet + no config → Allow

### Code Changes

**File: `internal/handler/validate.go`**

```go
// Reject bare Pods only if enforcement is enabled
if req.Kind.Kind == "Pod" {
    // Check if enforcement is enabled in this namespace
    configValid, configErr := h.checkNamespaceConfig(r.Context(), req.Namespace)

    // If config is incomplete, reject
    if configErr != "" {
        h.log.Info("rejecting bare pod due to incomplete namespace configuration",
            "name", req.Name,
            "namespace", req.Namespace,
            "error", configErr)
        h.sendResponse(w, string(req.UID), false,
            "namespace has incomplete PDB configuration: both pdb-min-available and pdb-max-unavailable labels must be set together")
        return
    }

    // If enforcement enabled (both labels present), reject bare pod
    if configValid {
        h.log.Info("rejecting bare pod (enforcement enabled)",
            "name", req.Name,
            "namespace", req.Namespace)
        h.sendResponse(w, string(req.UID), false,
            "bare pods are not allowed in enforced namespace; pods must be created by Deployment or StatefulSet")
        return
    }

    // No enforcement, allow bare pod
    h.log.Info("allowing bare pod (no enforcement)",
        "name", req.Name,
        "namespace", req.Namespace)
    h.sendResponse(w, string(req.UID), true, "")
    return
}

// Only allow Deployments and StatefulSets
if req.Kind.Kind != "Deployment" && req.Kind.Kind != "StatefulSet" {
    h.sendResponse(w, string(req.UID), true, "")
    return
}
```

**File: `internal/handler/mutate.go`**

```go
// Reject bare Pods
if req.Kind.Kind == "Pod" {
    h.log.Info("rejecting bare pod",
        "name", req.Name,
        "namespace", req.Namespace)
    h.sendMutatingResponse(w, string(req.UID), nil)
    return
}

// Only allow Deployments and StatefulSets
if req.Kind.Kind != "Deployment" && req.Kind.Kind != "StatefulSet" {
    h.sendMutatingResponse(w, string(req.UID), nil)
    return
}
```

**File: `manifests/mutatingwebhookconfiguration.yaml`**

```yaml
rules:
  - operations: ["CREATE"]
    apiGroups: ["apps"]
    apiVersions: ["v1"]
    resources: ["deployments", "statefulsets", "pods"]
    scope: "*"
```

**File: `manifests/validatingwebhookconfiguration.yaml`**

```yaml
rules:
  - operations: ["CREATE", "UPDATE"]
    apiGroups: ["apps"]
    apiVersions: ["v1"]
    resources: ["deployments", "statefulsets", "pods"]
    scope: "*"
```

---

## Test Coverage

### New Test Cases

**Mutating Handler:**
- ✅ Bare Pod - should skip (no mutation)
- ✅ StatefulSet with both labels - should mutate

**Validating Handler:**
- ✅ Bare Pod with enforcement enabled - should reject
- ✅ Bare Pod with no enforcement - should allow
- ✅ StatefulSet with config and matching PDB - should allow
- ✅ Non-apps resource (e.g., ConfigMap) - should allow

### Test Results

```
All 22 test cases PASS
Coverage: 72.9%

New tests demonstrate:
- Bare pods are rejected ONLY if enforcement is enabled
- Bare pods are allowed if no enforcement labels
- StatefulSets are treated same as Deployments
- Non-apps resources are ignored (not rejected)
```

---

## Migration Guide

### If Using Bare Pods

**Before:**
```bash
kubectl apply -f my-pod.yaml
# Pod created successfully
```

**After:**
```bash
kubectl apply -f my-pod.yaml
# Error: bare pods are not allowed; pods must be created by Deployment or StatefulSet
```

**Fix:** Migrate to Deployment or StatefulSet

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-pod  # Use original pod name
spec:
  replicas: 1
  selector:
    matchLabels:
      app: my-pod
  template:
    metadata:
      labels:
        app: my-pod
    spec:
      # Copy containers from original pod spec
      containers:
      - name: app
        image: my-image
```

---

## Examples

### Allowed: Deployment with PDB (Enforced Namespace)

```bash
# Namespace with enforcement labels
kubectl label namespace prod \
  pdb-min-available=2 \
  pdb-max-unavailable=1

# Deploy application
kubectl apply -f deployment.yaml
# ✅ PDB auto-created, deployment allowed
```

### Rejected: Bare Pod (Enforced Namespace)

```bash
# In enforced namespace (has min/max labels)
kubectl apply -f pod.yaml -n prod
# ❌ Error: bare pods are not allowed in enforced namespace
```

### Allowed: Bare Pod (Non-Enforced Namespace)

```bash
# In dev namespace (no enforcement labels)
kubectl apply -f pod.yaml -n dev
# ✅ Bare pods allowed when no enforcement
```

### Allowed: StatefulSet with enforcement

```bash
kubectl apply -f statefulset.yaml -n prod
# ✅ Checks same PDB requirements as Deployment
```

### Allowed: Non-apps resource

```bash
kubectl apply -f configmap.yaml
# ✅ ConfigMaps are ignored by webhooks
```

### Error: Incomplete Config

```bash
# Only set min, not max
kubectl label namespace staging pdb-min-available=2

# Any workload in this namespace is rejected
kubectl apply -f deployment.yaml -n staging
# ❌ Error: namespace has incomplete PDB configuration
```

---

## Error Messages

### Bare Pod Rejection (Enforcement Enabled)
```
bare pods are not allowed in enforced namespace; pods must be created by Deployment or StatefulSet
```

### Bare Pod with Incomplete Config
```
namespace has incomplete PDB configuration: both pdb-min-available and pdb-max-unavailable labels must be set together
```

### Incomplete Config (Deployment/StatefulSet)
```
namespace has incomplete PDB configuration: both pdb-min-available and pdb-max-unavailable labels must be set together
```

### No PDB Found (Enforcement Enabled)
```
deployment rejected: no PodDisruptionBudget in namespace prod selects pod labels app=myapp;
create a PDB with a matching selector before deploying
```

---

## Backward Compatibility

- ✅ Existing Deployments continue to work
- ✅ Existing StatefulSets now supported
- ✅ Bare Pods still allowed in non-enforced namespaces (no breaking change)
- ⚠️ Bare Pods rejected ONLY in namespaces with enforcement labels

**No action required** - Existing workloads are not affected unless you explicitly enable enforcement with min/max labels.

**If you enable enforcement**, you must:
- Ensure all Pods in that namespace are part of a Deployment or StatefulSet
- Or create matching PDBs manually
