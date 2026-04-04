# Namespace-Specific Mutation & Enforcement

## Overview

**Two independent labels** control webhook behavior per namespace:
- `pdb-webhook-mutate: "true"` → Auto-create PDB on deployment CREATE
- `pdb-webhook-validate: "true"` → Enforce PDB requirement on deployment CREATE/UPDATE

This allows **fine-grained control** for different use cases.

---

## Label Combinations

| Mutate | Validate | Behavior | Use Case |
|---|---|---|---|
| ❌ | ❌ | No PDB protection | Throwaway/testing namespaces |
| ✅ | ❌ | Auto-create PDB, but don't enforce | Dev namespaces (lenient) |
| ❌ | ✅ | Enforce PDB, no auto-creation | Strict production (manual PDBs only) |
| ✅ | ✅ | Auto-create + enforce | Standard production (safest) |

---

## Use Cases by Namespace Type

### 1. Production Namespace (Strictest)
```bash
kubectl create namespace production
kubectl label namespace production \
  pdb-webhook-mutate=true \
  pdb-webhook-validate=true
```

**Behavior:**
- ✅ Auto-creates PDB if missing
- ✅ Rejects deployments without matching PDB
- **Safest**: Enforcement + safety net

**Result:**
```bash
kubectl apply -f deployment-without-pdb.yaml -n production
# → Mutating webhook auto-creates PDB
# → Validating webhook sees PDB exists
# → ✅ Deployment ALLOWED
```

---

### 2. Staging Namespace (Lenient)
```bash
kubectl create namespace staging
kubectl label namespace staging \
  pdb-webhook-mutate=true
# pdb-webhook-validate NOT set
```

**Behavior:**
- ✅ Auto-creates PDB if missing
- ❌ Does NOT enforce validation
- **Lenient**: Lets you deploy without PDB if mutation fails

**Result:**
```bash
kubectl apply -f deployment-without-pdb.yaml -n staging
# → Mutating webhook auto-creates PDB (if namespace is labeled for auto-creation)
# → Validating webhook SKIPPED (no validate label)
# → ✅ Deployment ALLOWED (even if mutation failed)
```

---

### 3. Dev Namespace (Manual Control)
```bash
kubectl create namespace dev
kubectl label namespace dev \
  pdb-webhook-validate=true
# pdb-webhook-mutate NOT set
```

**Behavior:**
- ❌ No auto-creation
- ✅ Enforces PDB requirement
- **Strict validation**: Must create PDB manually before deploying

**Result:**
```bash
kubectl apply -f deployment-without-pdb.yaml -n dev
# → Mutating webhook SKIPPED (no mutate label)
# → Validating webhook enforces (has validate label)
# → ❌ Deployment REJECTED: no matching PDB

# Must create PDB manually first:
kubectl apply -f pdb.yaml -n dev
kubectl apply -f deployment-with-pdb.yaml -n dev
# → ✅ Deployment ALLOWED
```

---

### 4. Testing/Scratch Namespace (No Protection)
```bash
kubectl create namespace test-scratch
# No labels at all
```

**Behavior:**
- ❌ No auto-creation
- ❌ No enforcement
- **Completely open**: Deploy anything without PDB requirements

**Result:**
```bash
kubectl apply -f deployment-without-pdb.yaml -n test-scratch
# → Both webhooks SKIPPED (no labels)
# → ✅ Deployment ALLOWED (no restrictions)
```

---

## Configuration Examples

### Setup All Namespaces

```bash
# Production (full protection)
kubectl create namespace production
kubectl label namespace production \
  pdb-webhook-mutate=true \
  pdb-webhook-validate=true

# Staging (auto-create only)
kubectl create namespace staging
kubectl label namespace staging pdb-webhook-mutate=true

# Dev (validate only)
kubectl create namespace dev
kubectl label namespace dev pdb-webhook-validate=true

# Testing (no protection)
kubectl create namespace test
# No labels

# System (no protection)
kubectl create namespace webhook-system
# No labels (already exists, no need to label)
```

---

## Check Namespace Configuration

### View single namespace labels
```bash
kubectl get namespace production --show-labels
```

Expected output:
```
NAME         STATUS   AGE   LABELS
production   Active   5m    pdb-webhook-mutate=true,pdb-webhook-validate=true
```

### View all namespaces with their webhook settings
```bash
kubectl get ns -o wide --show-labels
```

Or formatted:
```bash
kubectl get ns -o custom-columns=\
NAME:.metadata.name,\
MUTATE:.metadata.labels.pdb-webhook-mutate,\
VALIDATE:.metadata.labels.pdb-webhook-validate
```

Output:
```
NAME             MUTATE   VALIDATE
production       true     true
staging          true     <none>
dev              <none>   true
test-scratch     <none>   <none>
webhook-system   <none>   <none>
kube-system      <none>   <none>
```

### Count namespaces by enforcement type
```bash
echo "=== Mutation Enabled ==="
kubectl get ns -l pdb-webhook-mutate=true --no-headers | wc -l

echo "=== Validation Enabled ==="
kubectl get ns -l pdb-webhook-validate=true --no-headers | wc -l

echo "=== Both Enabled ==="
kubectl get ns -l pdb-webhook-mutate=true,pdb-webhook-validate=true --no-headers | wc -l

echo "=== No Protection ==="
kubectl get ns -L pdb-webhook-mutate,pdb-webhook-validate | grep -v true
```

---

## Testing Different Scenarios

### Scenario 1: Production with Auto-PDB
```bash
# Setup
kubectl label namespace default pdb-webhook-mutate=true pdb-webhook-validate=true --overwrite

# Test: Deploy without explicit PDB
kubectl apply -f test/deployment-auto-pdb.yaml

# Expected: ✅ ALLOWED
# - Mutating webhook auto-creates PDB
# - Validating webhook sees PDB exists
# - Deployment succeeds

# Verify
kubectl get deployment nginx-auto-pdb
kubectl get pdb nginx-auto-pdb
```

### Scenario 2: Dev with Manual PDB Only
```bash
# Setup
kubectl create namespace dev-strict
kubectl label namespace dev-strict pdb-webhook-validate=true

# Test: Deploy without PDB
kubectl apply -f test/deployment-without-pdb.yaml -n dev-strict

# Expected: ❌ REJECTED
# - Mutating webhook skipped (no mutate label)
# - Validating webhook enforces (has validate label)
# - Error: no matching PDB

# Fix: Create PDB manually
kubectl apply -f test/deployment-with-pdb.yaml -n dev-strict

# Expected: ✅ ALLOWED
# - PDB exists and matches
# - Deployment succeeds
```

### Scenario 3: Testing Namespace (No Restrictions)
```bash
# Setup
kubectl create namespace testing
# No labels at all

# Test: Deploy without PDB
kubectl apply -f test/deployment-without-pdb.yaml -n testing

# Expected: ✅ ALLOWED
# - No webhooks active
# - Deployment succeeds without PDB
```

### Scenario 4: Toggle Enforcement
```bash
# Setup
kubectl create namespace toggle-test
kubectl label namespace toggle-test pdb-webhook-mutate=true

# Test 1: Deploy without PDB (should succeed - no validation)
kubectl apply -f test/deployment-without-pdb.yaml -n toggle-test
# ✅ Result: ALLOWED

# Enable validation
kubectl label namespace toggle-test pdb-webhook-validate=true

# Test 2: Try to deploy without PDB (should fail - validation on)
kubectl apply -f test/deployment-without-pdb.yaml -n toggle-test
# ❌ Result: REJECTED

# Disable validation
kubectl label namespace toggle-test pdb-webhook-validate- --overwrite

# Test 3: Deploy without PDB (should succeed again - validation off)
kubectl apply -f test/deployment-without-pdb.yaml -n toggle-test
# ✅ Result: ALLOWED
```

---

## Modify Labels After Creation

### Add mutation to existing namespace
```bash
kubectl label namespace staging pdb-webhook-mutate=true
```

### Remove mutation from namespace
```bash
kubectl label namespace staging pdb-webhook-mutate- --overwrite
```

### Toggle validation on/off
```bash
# Enable
kubectl label namespace dev pdb-webhook-validate=true

# Disable
kubectl label namespace dev pdb-webhook-validate- --overwrite
```

### Replace all labels at once
```bash
# Remove all webhook labels from namespace
kubectl label namespace myns pdb-webhook-mutate- pdb-webhook-validate- --overwrite

# Set specific labels
kubectl label namespace myns pdb-webhook-mutate=true pdb-webhook-validate=true --overwrite
```

---

## Matrix: What Happens in Each Scenario

### Production (mutate=true, validate=true)
```
Deploy WITHOUT PDB:
  1. Mutate webhook: Auto-creates matching PDB ✅
  2. Validate webhook: Sees PDB exists ✅
  Result: ✅ ALLOWED

Deploy WITH PDB:
  1. Mutate webhook: Detects existing PDB, skips creation ✅
  2. Validate webhook: Sees PDB exists ✅
  Result: ✅ ALLOWED
```

### Staging (mutate=true, validate=false)
```
Deploy WITHOUT PDB:
  1. Mutate webhook: Auto-creates matching PDB ✅
  Result: ✅ ALLOWED

Deploy WITH PDB:
  1. Mutate webhook: Detects existing PDB, skips creation ✅
  Result: ✅ ALLOWED
```

### Dev (mutate=false, validate=true)
```
Deploy WITHOUT PDB:
  1. Validate webhook: No PDB found ❌
  Result: ❌ REJECTED - must create PDB manually

Deploy WITH PDB:
  1. Validate webhook: PDB found ✅
  Result: ✅ ALLOWED
```

### Testing (mutate=false, validate=false)
```
Deploy WITHOUT PDB:
  Result: ✅ ALLOWED (no restrictions)

Deploy WITH PDB:
  Result: ✅ ALLOWED (no restrictions)
```

