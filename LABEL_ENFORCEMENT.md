# Label-Based Webhook Enforcement

## Overview

The webhook is now **label-based** instead of namespace-name-based. This means:
- Only namespaces with label `pdb-webhook-enforce: "true"` are enforced
- Throwaway/temporary namespaces won't be enforced unless explicitly labeled
- More control and flexibility

## How It Works

### Before (Name-based)
```
All namespaces enforced EXCEPT webhook-system and kube-system
```

### After (Label-based)
```
Only namespaces with label pdb-webhook-enforce: "true" are enforced
```

---

## Enable Enforcement on a Namespace

### Add the label to enable webhook
```bash
kubectl label namespace production pdb-webhook-enforce=true
kubectl label namespace staging pdb-webhook-enforce=true
kubectl label namespace default pdb-webhook-enforce=true
```

### Verify label is set
```bash
kubectl get namespace production --show-labels
```

Output includes:
```
pdb-webhook-enforce=true
```

---

## Check Which Namespaces Are Enforced

```bash
# List all namespaces with enforcement enabled
kubectl get namespaces -l pdb-webhook-enforce=true

# Output:
NAME          STATUS   AGE
default       Active   5m
production    Active   2m
staging       Active   1m
```

---

## Disable Enforcement on a Namespace

Remove the label to disable webhook:
```bash
kubectl label namespace throwaway pdb-webhook-enforce- --overwrite
```

Or use the delete syntax:
```bash
kubectl label namespace throwaway pdb-webhook-enforce-
```

Verify it's removed:
```bash
kubectl get namespace throwaway --show-labels
# Should NOT show pdb-webhook-enforce=true
```

---

## Enforcement Status by Namespace

| Namespace | Label | Enforcement | Notes |
|---|---|---|---|
| default | `pdb-webhook-enforce=true` | ✅ YES | Must have PDB |
| production | `pdb-webhook-enforce=true` | ✅ YES | Must have PDB |
| staging | `pdb-webhook-enforce=true` | ✅ YES | Must have PDB |
| test-temp | (no label) | ❌ NO | Throwaway namespace |
| dev-scratch | (no label) | ❌ NO | Temporary testing |
| webhook-system | (no label) | ❌ NO | System namespace |
| kube-system | (no label) | ❌ NO | System namespace |

---

## Testing Enforcement

### Setup: Label the default namespace
```bash
kubectl label namespace default pdb-webhook-enforce=true --overwrite
```

### Test 1: Deploy WITH PDB (should succeed)
```bash
kubectl apply -f test/deployment-with-pdb.yaml
```
✅ Expected: ALLOWED

### Test 2: Deploy WITHOUT PDB (should fail)
```bash
kubectl apply -f test/deployment-without-pdb.yaml
```
❌ Expected: REJECTED (no matching PDB)

### Test 3: Create a throwaway namespace (no enforcement)
```bash
kubectl create namespace temp-test
kubectl apply -f test/deployment-without-pdb.yaml -n temp-test
```
✅ Expected: ALLOWED (no enforcement because no label)

### Test 4: Enable enforcement on throwaway namespace
```bash
kubectl label namespace temp-test pdb-webhook-enforce=true
kubectl apply -f test/deployment-without-pdb.yaml -n temp-test
```
❌ Expected: REJECTED (now enforced)

---

## Best Practices

### 1. Label namespaces at creation time
```bash
kubectl create namespace production \
  -l pdb-webhook-enforce=true
```

Or with kubectl:
```bash
kubectl create namespace production
kubectl label namespace production pdb-webhook-enforce=true
```

### 2. Only label production/stable namespaces
```
✅ DO label: production, staging, default, shared-services
❌ DON'T label: temp, test, scratch, throwaway, dev-personal
```

### 3. Document enforcement status
```bash
# View all enforced namespaces
kubectl get ns -l pdb-webhook-enforce=true -o wide

# View non-enforced namespaces
kubectl get ns -L pdb-webhook-enforce
```

### 4. Validate before labeling
```bash
# Check all deployments in namespace have PDBs
kubectl get deployment -n production
kubectl get pdb -n production

# Before labeling, ensure coverage
for dep in $(kubectl get deployments -n production -o name); do
  # Check each has a matching PDB
done
```

---

## Troubleshooting

### Webhook not enforcing (expected to enforce but isn't)

Check if namespace has the label:
```bash
kubectl get namespace myns --show-labels | grep pdb-webhook-enforce
```

If not present, add it:
```bash
kubectl label namespace myns pdb-webhook-enforce=true
```

### Webhook is enforcing (expected to not enforce but is)

Check if namespace incorrectly has the label:
```bash
kubectl get namespace myns --show-labels | grep pdb-webhook-enforce
```

If present, remove it:
```bash
kubectl label namespace myns pdb-webhook-enforce- --overwrite
```

### Check all namespace labels
```bash
# Show all namespaces with custom labels
kubectl get ns --show-labels

# Format output
kubectl get ns -o wide
```

