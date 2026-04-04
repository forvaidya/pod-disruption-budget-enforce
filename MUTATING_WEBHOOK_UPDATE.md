# Mutating Webhook Update: Namespace Label-based Configuration

## Summary

Updated the mutating webhook to use **namespace labels** for PDB auto-creation configuration instead of ConfigMaps. This provides:
- **Opt-in auto-creation**: Explicit, declarative control per namespace
- **Label-based**: No ConfigMap management needed
- **Clear intent**: Namespace labels show exactly what will happen

---

## Changes Made

### 1. Mutating Webhook Logic (`internal/handler/mutate.go`)

**New method: `getNamespacePDBLabels()`**
- Reads namespace labels instead of ConfigMap
- Looks for: `pdb-webhook.awanipro.com/min-available` and `pdb-webhook.awanipro.com/max-unavailable`
- **Returns `false` if labels don't exist** → No auto-creation
- Parses labels and returns min/max values if found

**Updated `Handle()` method:**
- First checks if namespace has PDB configuration labels
- If labels missing → Skips mutation (returns early)
- If labels exist → Creates PDB with those values

**Updated `createPDB()` method:**
- Now accepts `minAvailable` and `maxUnavailable` parameters
- Uses values from namespace labels

### 2. Kubernetes Manifests

**`manifests/pdb-config-example.yaml`** - Completely rewritten
- Old: ConfigMap-based configuration
- New: Namespace label-based configuration
- Shows 4 example namespaces:
  1. Default with auto-creation (minAvailable: 2, maxUnavailable: 4)
  2. Staging with lenient settings (minAvailable: 1, maxUnavailable: 2)
  3. Production with strict settings (minAvailable: 3, maxUnavailable: 1)
  4. No-auto namespace with NO labels (manual PDBs required)

### 3. Documentation Updates

**`README.md`**
- Updated "Overview" to explain opt-in auto-creation
- Updated "PDB Auto-creation (Opt-in Per-Namespace)" section with:
  - How to enable labels on a namespace
  - Example namespace configurations
  - Label-based approach explanation
- Updated "Quick Start" to show label enablement
- Updated "Webhook Endpoints" to explain label checking

**`DEPLOYMENT.md`**
- Updated "Webhook Order and Behavior" to explain label checking
- Updated Test 4 to show namespace label configuration
- Added Test 5 to show namespace WITHOUT auto-creation
- Updated prerequisite sections

---

## Behavior

### Deployment Creation Flow

```
┌─ Deployment CREATE request
│
├─ Mutating Webhook checks:
│  └─ Does namespace have these labels?
│     ├─ pdb-webhook.awanipro.com/min-available: <value>
│     └─ pdb-webhook.awanipro.com/max-unavailable: <value>
│
├─ If YES:
│  └─ Auto-create PDB with label values
│
├─ If NO:
│  └─ Skip mutation (no PDB created)
│
└─ Validating Webhook ensures:
   └─ Deployment has a matching PDB
      ├─ If exists → ALLOW
      └─ If missing → REJECT
```

---

## Configuration Examples

### Enable Auto-creation (Current Deployment)

```bash
# Add labels to default namespace
kubectl label namespace default \
  pdb-webhook.awanipro.com/min-available=2 \
  pdb-webhook.awanipro.com/max-unavailable=4 \
  --overwrite

# From now on, deployments in default will get auto-created PDBs
kubectl apply -f test/deployment-auto-pdb.yaml  # Succeeds, PDB auto-created
```

### Create New Namespace with Auto-creation

```bash
kubectl create namespace staging
kubectl label namespace staging \
  pdb-webhook.awanipro.com/min-available=1 \
  pdb-webhook.awanipro.com/max-unavailable=2
```

### Create Namespace WITHOUT Auto-creation

```bash
kubectl create namespace strict
# No labels = no auto-creation
# Deployments must have explicit PDBs
```

---

## Implementation Details

### Label Names
- `pdb-webhook.awanipro.com/min-available` — Number of pods that must always be available
- `pdb-webhook.awanipro.com/max-unavailable` — Number of pods that can be temporarily unavailable

### Label Values
- Must be parseable as integers (e.g., "1", "2", "3")
- Currently supports integer values only (no percentages)
- Invalid values cause auto-creation to skip gracefully (logged as error)

### Webhook Behavior

**If namespace labels exist:**
1. Parse min-available and max-unavailable values
2. Check if matching PDB already exists
3. If not, create PDB with deployment name and labels from namespace
4. Set ownership reference so PDB is deleted with deployment

**If namespace labels missing:**
1. Skip mutation entirely
2. Validating webhook will enforce PDB requirement (reject if missing)

### Error Handling

- **Missing labels**: Skip mutation (no error)
- **Invalid label values**: Log error, skip mutation
- **Cannot read namespace**: Log error, skip mutation
- **Cannot create PDB**: Log error, skip mutation (validating webhook will catch it)

---

## Testing

### Test Case 1: Auto-creation (namespace has labels)
```bash
kubectl label namespace default pdb-webhook.awanipro.com/min-available=2 pdb-webhook.awanipro.com/max-unavailable=4
kubectl apply -f test/deployment-auto-pdb.yaml
# Expected: Deployment + PDB both created
```

### Test Case 2: Explicit PDB (always works)
```bash
kubectl apply -f test/deployment-with-pdb.yaml
# Expected: Deployment + explicit PDB created
```

### Test Case 3: No auto-creation (namespace has no labels)
```bash
kubectl create namespace test-no-auto
kubectl apply -f - <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: nginx
  namespace: test-no-auto
spec:
  replicas: 3
  selector:
    matchLabels:
      app: nginx
  template:
    metadata:
      labels:
        app: nginx
    spec:
      containers:
        - name: nginx
          image: nginx
EOF
# Expected: REJECTED (validating webhook, no PDB exists)
```

---

## Migration Path (if switching from ConfigMap)

If you had ConfigMaps before, migrate to labels:

```bash
# Remove old ConfigMap approach
kubectl delete configmap pdb-config -n <namespace>

# Add labels instead
kubectl label namespace <namespace> \
  pdb-webhook.awanipro.com/min-available=<value> \
  pdb-webhook.awanipro.com/max-unavailable=<value>
```

---

## Benefits

1. **Declarative**: Namespace labels are visible with `kubectl describe namespace`
2. **No extra resources**: No ConfigMap to manage
3. **Opt-in**: Clear which namespaces have auto-creation enabled
4. **RBAC-friendly**: Label changes can be controlled separately from ConfigMap access
5. **Clear intent**: Labels express "this namespace auto-creates PDBs"
