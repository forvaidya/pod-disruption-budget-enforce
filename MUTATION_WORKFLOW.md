# Deployment Mutation Workflow - `nginx-auto-pdb`

## Initial Deployment (Pre-Mutation)

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: nginx-auto-pdb
  namespace: default
  labels:
    app: nginx-auto-pdb
spec:
  replicas: 3
  selector:
    matchLabels:
      app: nginx-auto-pdb
  template:
    metadata:
      labels:
        app: nginx-auto-pdb  # ← Selector for PDB matching
    spec:
      containers:
        - name: nginx
          image: nginx:1.26-alpine
          ports:
            - containerPort: 80
          resources:
            requests:
              cpu: 100m
              memory: 64Mi
            limits:
              cpu: 500m
              memory: 128Mi
```

**Key Characteristics:**
- **No explicit PDB** - Only deployment, no PodDisruptionBudget
- **3 replicas** - Expected to scale to 3 pods
- **Pod template labels** - `app: nginx-auto-pdb` (important for PDB matching)
- **Namespace** - `default`

---

## Mutation Process

### Step 1: Kubernetes API Server receives CREATE request

User runs:
```bash
kubectl apply -f test/deployment-auto-pdb.yaml
```

API Server prepares `AdmissionReview` and sends to **MutatingWebhookConfiguration** (pdb-webhook-mutate)

---

### Step 2: Mutating Webhook Inspection

The webhook receives the deployment and performs these checks:

#### Check 1: Is this a CREATE operation on a Deployment?
```
✓ Kind: Deployment
✓ Operation: CREATE
✓ Group: apps
```

#### Check 2: Does the NAMESPACE have PDB auto-creation labels?

**Current namespace labels:**
```bash
kubectl get namespace default --show-labels
```

Expected labels for auto-creation:
```
pdb-webhook.awanipro.com/min-available=1
pdb-webhook.awanipro.com/max-unavailable=2
```

**If labels are PRESENT:**
- ✅ Proceed to PDB auto-creation
- Extract values: `minAvailable: 1`, `maxUnavailable: 2`

**If labels are ABSENT:**
- ⏭️ Skip mutation (no PDB created)
- Deployment passes through unchanged
- Validating webhook will then reject it (no matching PDB)

---

### Step 3: Check if PDB Already Exists

Webhook queries all PDBs in the `default` namespace:

```bash
kubectl get pdb -n default
```

**Does a PDB match the deployment's pod labels?**
- **Pod Labels to Match:** `app: nginx-auto-pdb`
- **Existing PDBs:** Check all PDBs, look for selector matching `app: nginx-auto-pdb`

If a matching PDB already exists:
- ✅ Skip auto-creation (don't create duplicate)
- Deployment proceeds unchanged

If no matching PDB exists:
- ➕ Create a new PDB (see next step)

---

### Step 4: Auto-Create PDB

The webhook **creates** a new PodDisruptionBudget:

```yaml
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: nginx-auto-pdb  # Same name as deployment
  namespace: default
  labels:
    app.kubernetes.io/name: pdb-webhook
    app.kubernetes.io/component: admission-controller
    app.kubernetes.io/managed-by: pdb-webhook-mutator
  ownerReferences:
    - apiVersion: apps/v1
      kind: Deployment
      name: nginx-auto-pdb
      uid: <deployment-uid>  # Links to the deployment
      controller: true
      blockOwnerDeletion: true
spec:
  minAvailable: 1          # ← From namespace label
  maxUnavailable: 2        # ← From namespace label
  selector:
    matchLabels:
      app: nginx-auto-pdb  # ← Matches pod template labels
```

**What this PDB does:**
- 📌 **Min Available**: At least 1 pod must always be available
- 📌 **Max Unavailable**: At most 2 pods can be disrupted simultaneously
- 📌 **Selector**: Protects all pods labeled `app: nginx-auto-pdb`
- 📌 **Owner**: Deployment owns the PDB (deletes PDB when deployment is deleted)

---

## Final State After Mutation

### Resources Created:

**1. Deployment**
```bash
$ kubectl get deployment nginx-auto-pdb
NAME             READY   UP-TO-DATE   AVAILABLE   AGE
nginx-auto-pdb   3/3     3            3           10s
```

**2. Auto-Created PDB**
```bash
$ kubectl get pdb nginx-auto-pdb
NAME             MIN AVAILABLE   MAX UNAVAILABLE   ALLOWED DISRUPTIONS   AGE
nginx-auto-pdb   1               2                 1                     10s
```

**3. Pods**
```bash
$ kubectl get pods -l app=nginx-auto-pdb
NAME                            READY   STATUS    RESTARTS   AGE
nginx-auto-pdb-abc123-def45     1/1     Running   0          10s
nginx-auto-pdb-abc123-ghi67     1/1     Running   0          10s
nginx-auto-pdb-abc123-jkl89     1/1     Running   0          10s
```

---

## Mutation Timeline

```
1. kubectl apply deployment-auto-pdb.yaml
   ↓
2. Kubernetes API Server validates request
   ↓
3. MutatingWebhookConfiguration intercepted
   ├─ Check: Is this a Deployment CREATE? ✓
   ├─ Check: Does namespace have PDB labels? ✓ (if labeled)
   ├─ Check: Does PDB already exist? ✗ (first time)
   └─ Action: Create PDB with minAvailable=1, maxUnavailable=2
   ↓
4. Webhook returns mutated admission response
   ├─ UID: (same request ID)
   ├─ Allowed: true
   └─ Patches: (empty - created PDB via API call, not JSON patch)
   ↓
5. ValidatingWebhookConfiguration checks
   ├─ Does a PDB exist matching pod labels? ✓ (just created)
   └─ Decision: ALLOW deployment
   ↓
6. Deployment is created
   ├─ 3 pods scheduled and running
   └─ PDB active and protecting them
```

---

## Why This Pattern Works

### Without Mutation (Old Way)
```
User creates Deployment WITHOUT PDB
→ Validating webhook rejects it ❌
→ User must manually create PDB
→ Then re-apply deployment
```

### With Mutation (New Way)
```
User creates Deployment WITHOUT PDB
→ Mutating webhook auto-creates matching PDB ✅
→ Validating webhook sees PDB exists ✓
→ Deployment succeeds immediately 🎉
```

---

## Testing the Mutation

### Setup: Label the namespace for auto-creation
```bash
kubectl label namespace default \
  pdb-webhook.awanipro.com/min-available=1 \
  pdb-webhook.awanipro.com/max-unavailable=2
```

### Verify labels are set
```bash
kubectl get namespace default --show-labels
```

Expected output includes:
```
pdb-webhook.awanipro.com/max-unavailable=2
pdb-webhook.awanipro.com/min-available=1
```

### Apply the auto-pdb deployment
```bash
kubectl apply -f test/deployment-auto-pdb.yaml
```

### Check mutation worked
```bash
# Deployment should exist
kubectl get deployment nginx-auto-pdb

# PDB should be auto-created
kubectl get pdb nginx-auto-pdb

# Check PDB details
kubectl describe pdb nginx-auto-pdb

# View pod protection
kubectl get pdb nginx-auto-pdb -o yaml
```

### Expected Result
```
✅ Deployment ALLOWED (created successfully)
✅ PDB auto-created with minAvailable=1, maxUnavailable=2
✅ 3 pods running
✅ PDB actively protecting pods
```

---

## Edge Cases

### Case 1: Namespace NOT labeled
```
→ Mutating webhook skips (no labels)
→ Validating webhook rejects ❌
→ Result: DEPLOYMENT REJECTED
```

### Case 2: PDB already exists before deployment
```
→ Mutating webhook detects existing PDB
→ Skips creation (prevents duplicate)
→ Validating webhook sees PDB ✓
→ Result: DEPLOYMENT ALLOWED
```

### Case 3: Mutating webhook fails
```
→ failurePolicy: Ignore (continues despite failure)
→ Deployment proceeds without auto-created PDB
→ Validating webhook rejects it ❌
→ Result: DEPLOYMENT REJECTED (caught by safety net)
```

### Case 4: Multiple deployments in labeled namespace
```
→ Each gets its own auto-created PDB
→ Names match: pdb-webhook, app-staging, etc.
→ All protected with consistent rules
```

