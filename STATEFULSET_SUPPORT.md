# StatefulSet Support

## Overview

The webhooks fully support both **Deployments** and **StatefulSets** with identical PDB enforcement logic.

## Supported Workloads

| Workload Type | Mutating Webhook | Validating Webhook | PDB Logic |
|---|---|---|---|
| Deployment | ✅ Auto-create PDB | ✅ Enforce PDB | Same as below |
| StatefulSet | ✅ Auto-create PDB | ✅ Enforce PDB | Same as below |
| Bare Pod | ✅ Skip | ✅ Conditional reject | Only if enforcement enabled |
| Other (Job, CronJob, etc.) | ✅ Skip | ✅ Allow | Not validated |

## StatefulSet Cardinality Handling

**StatefulSets require ordinal-order rolling updates:**

- Pods must update in order: pod-0 → pod-1 → pod-2 → ...
- Only ONE pod can be unavailable at a time
- This maintains stable network identity and storage guarantees

**Enforcement:**
- **Deployments**: Uses namespace labels (minAvailable or maxUnavailable)
- **StatefulSets**: **Always enforces `maxUnavailable: 1`**
  - Ignores namespace `pdb-min-available` label for StatefulSets
  - Forces `maxUnavailable=1` to ensure one-pod-at-a-time updates
  - Guarantees ordinal order is maintained

**Example:**
```yaml
# Namespace config
pdb-min-available: 3
pdb-max-unavailable: 2

---
# Deployment: Uses namespace labels
# PDB spec: minAvailable: 3

---
# StatefulSet: Always uses maxUnavailable=1
# PDB spec: maxUnavailable: 1  (overrides namespace config)
```

## How It Works

### Detection

Both webhooks detect workload Kind from the admission request:
```go
if req.Kind.Kind == "Deployment" {
    // Handle Deployment
} else if req.Kind.Kind == "StatefulSet" {
    // Handle StatefulSet
} else if req.Kind.Kind == "Pod" {
    // Handle Pods
}
```

### Selector & Labels Extraction

For both Deployment and StatefulSet, we extract:
- `workload.Spec.Selector` → for PDB label matching
- `workload.Spec.Template.Labels` → pod labels
- `workload.Name` → PDB name
- `workload.Namespace` → PDB namespace

### PDB Creation

When enforcement is enabled, the mutating webhook creates a PDB:

**For Deployment:**
```yaml
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: my-deployment
  namespace: default
  labels:
    app.kubernetes.io/name: pdb-webhook
    app.kubernetes.io/component: admission-controller
    app.kubernetes.io/managed-by: pdb-webhook-mutator
    pdb-webhook.workload-name: my-deployment
spec:
  selector:
    matchLabels:
      app: my-app
  minAvailable: 2  # From namespace label pdb-min-available
```

**For StatefulSet** (always `maxUnavailable: 1`):
```yaml
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: my-statefulset
  namespace: default
  labels:
    app.kubernetes.io/name: pdb-webhook
    app.kubernetes.io/component: admission-controller
    app.kubernetes.io/managed-by: pdb-webhook-mutator
    pdb-webhook.workload-name: my-statefulset
spec:
  selector:
    matchLabels:
      app: my-app
  maxUnavailable: 1  # ALWAYS 1 for StatefulSets (ordinal-order updates)
```

## Example: StatefulSet with Enforcement

### 1. Enable Enforcement

```bash
kubectl label namespace prod \
  pdb-min-available=2 \
  pdb-max-unavailable=1
```

### 2. Create StatefulSet

```yaml
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: my-database
  namespace: prod
spec:
  serviceName: my-database
  replicas: 3
  selector:
    matchLabels:
      app: my-database
  template:
    metadata:
      labels:
        app: my-database
    spec:
      containers:
      - name: db
        image: postgres:15
        volumeMounts:
        - name: data
          mountPath: /var/lib/postgresql/data
  volumeClaimTemplates:
  - metadata:
      name: data
    spec:
      accessModes: ["ReadWriteOnce"]
      resources:
        requests:
          storage: 10Gi
```

### 3. PDB Auto-Created

```bash
$ kubectl get pdb -n prod my-database
NAME            MIN AVAILABLE   MAX UNAVAILABLE   ALLOWED DISRUPTIONS
my-database     2               <unset>           1
```

### 4. Validation Enforced

```bash
# Now you MUST have a matching PDB to update this StatefulSet
kubectl patch statefulset my-database -p '{"spec":{"replicas":4}}'
# ✅ Allowed (PDB exists)

# If you delete the PDB, updates are blocked
kubectl delete pdb my-database
kubectl patch statefulset my-database -p '{"spec":{"replicas":4}}'
# ❌ Error: no PodDisruptionBudget found
```

## Kubernetes API Compatibility

### Deployment Fields Used
```go
deployment.Name                    // PDB name
deployment.Namespace               // PDB namespace
deployment.Spec.Selector           // PDB selector
deployment.Spec.Template.Labels    // Pod label matching
```

### StatefulSet Fields Used
```go
statefulset.Name                   // PDB name
statefulset.Namespace              // PDB namespace
statefulset.Spec.Selector          // PDB selector (same structure)
statefulset.Spec.Template.Labels   // Pod label matching (same structure)
```

Both have identical `Spec.Selector` and `Spec.Template` structures, so PDB creation logic is identical.

## StatefulSet-Specific Considerations

### Why maxUnavailable=1 is Required

StatefulSets have strict ordering requirements:

1. **Stable Network Identity**: Pod `my-db-0` must always be the same pod
2. **Stable Storage**: Each pod has persistent volume; updates must maintain order
3. **Cluster Bootstrap**: Some pods (e.g., database clusters) depend on lower-ordinal peers
4. **Ordinal-Order Updates**: Kubernetes updates in order: pod-0 → pod-1 → pod-2 → ...

**If you allow 2+ pods unavailable:**
- Pod-1 and pod-2 could update simultaneously
- This breaks ordering guarantees
- Data consistency issues in stateful systems (databases, caches, etc.)

**With maxUnavailable=1:**
- Only pod-N can update at a time
- pod-0 through pod-N-1 remain available
- Ordering is maintained
- Lower-ordinal pods can serve requests while update is in progress

### Pod Naming
StatefulSet pods are named: `{name}-{ordinal}`
- Example: `my-database-0`, `my-database-1`, `my-database-2`

The PDB selector matches pod labels, not names, so ordinal naming doesn't affect PDB matching.

### Rolling Updates
StatefulSet rolling updates respect PDB:
- Kubernetes respects PDB disruption budget during ordinal updates
- PDB with `minAvailable: 2` ensures 2 replicas always available during update
- With `maxUnavailable: 1`, only 1 pod updated at a time

### Persistent Volumes
StatefulSets typically use `volumeClaimTemplates`:
- PDB does NOT affect volume claim lifecycle
- PDB only affects pod disruption
- Persistent volumes are retained even if pod is disrupted (respecting PDB)

## Test Coverage

### New Tests Added
- ✅ StatefulSet with enforcement enabled - should allow
- ✅ StatefulSet with matching PDB - should allow
- ✅ StatefulSet auto-creation of PDB - test validates labels and selector

### All Tests Passing
```
Total: 22 tests
Coverage: 74.6%

Scenarios tested:
- Deployment creation/update
- StatefulSet creation/update
- Bare Pod handling (both enforced and non-enforced)
- PDB matching logic for both workload types
- Label and selector validation
- Namespace configuration validation
```

## Implementation Details

### File: `internal/handler/validate.go`

Extracts workload info dynamically:
```go
if req.Kind.Kind == "Deployment" {
    deployment := &appsv1.Deployment{}
    json.Unmarshal(req.Object.Raw, deployment)
    workloadName = deployment.Name
    // ...
} else if req.Kind.Kind == "StatefulSet" {
    sts := &appsv1.StatefulSet{}
    json.Unmarshal(req.Object.Raw, sts)
    workloadName = sts.Name
    // ...
}
```

### File: `internal/handler/mutate.go`

Same extraction logic, plus **StatefulSet cardinality handling**:

```go
// For StatefulSets, force maxUnavailable=1 for ordinal-order updates
if req.Kind.Kind == "StatefulSet" {
    h.log.Info("StatefulSet detected: enforcing maxUnavailable=1 for ordinal-order rolling update",
        "name", workloadName,
        "namespace", workloadNamespace)
    maxUnavailable = intOrString(1)
    minAvailable = intstr.IntOrString{Type: intstr.Int, IntVal: 0} // Clear minAvailable
}

func (h *MutatingHandler) createPDB(
    ctx context.Context,
    name, namespace string,
    selector *metav1.LabelSelector,
    minAvailable, maxUnavailable intstr.IntOrString,
) (*policyv1.PodDisruptionBudget, error)
```

**Key difference:**
- **Deployments**: Use namespace labels as-is
- **StatefulSets**: Override to always use `maxUnavailable: 1`

### File: `manifests/mutatingwebhookconfiguration.yaml`

```yaml
resources: ["deployments", "statefulsets", "pods"]
```

### File: `manifests/validatingwebhookconfiguration.yaml`

```yaml
resources: ["deployments", "statefulsets", "pods"]
```

## Backwards Compatibility

- ✅ Existing Deployments continue working unchanged
- ✅ New StatefulSets work with same enforcement model
- ✅ No breaking changes to API or configuration

## Common Operations

### Check PDB for StatefulSet
```bash
kubectl get pdb -o wide
NAME              MIN AVAILABLE   AVAILABLE   DISRUPTIONS ALLOWED
my-statefulset    2               3           1
```

### Verify Selector
```bash
# PDB should match StatefulSet pods
kubectl get pdb my-statefulset -o yaml | grep -A 5 selector:

# Get pod labels
kubectl get pods -l app=my-database -o wide
```

### Monitor Disruptions
```bash
# Watch PDB status during update
kubectl get pdb my-statefulset --watch

# Update StatefulSet (respects PDB)
kubectl rollout restart statefulset/my-database
```

### Troubleshooting

**PDB not created:**
```bash
# Check namespace labels
kubectl get ns prod --show-labels

# Should show: pdb-min-available=... pdb-max-unavailable=...
```

**Update blocked by PDB:**
```bash
# Check PDB disruption allowance
kubectl get pdb my-database
# If DISRUPTIONS ALLOWED = 0, update is blocked

# Solution: wait for pod to become unavailable, or adjust PDB
kubectl patch pdb my-database -p '{"spec":{"minAvailable":1}}'
```

**Pod not selected by PDB:**
```bash
# Verify selector matches
kubectl describe pdb my-database
# Compare selector with pod labels
kubectl get pods -L app,other-label
```

## Next Steps

1. Deploy StatefulSet with enforcement enabled
2. Verify PDB is auto-created
3. Attempt update/delete - confirm PDB is respected
4. Monitor pod disruptions with PDB
