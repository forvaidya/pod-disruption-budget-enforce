# Implementation Flow: Label-Based Enforcement

## Code Flow Diagram

```
Deployment CREATE request
         ↓
    ┌─────────────────────────────────────┐
    │   Mutating Webhook (/mutate)        │
    │   - Only runs on CREATE              │
    │   - failurePolicy: Ignore            │
    └─────────────────────────────────────┘
         ↓
    getNamespacePDBLabels()
         ↓
    ┌──────────────────────────────────────────────┐
    │ Check: min label exists? max label exists?   │
    └──────────────────────────────────────────────┘
         ↓
    ┌─────────────────────────────────────────────────────────────────┐
    │  hasMin != hasMax?  (incomplete config)                          │
    │  YES: return error → skip mutation, allow deployment             │
    │  NO: continue                                                    │
    └─────────────────────────────────────────────────────────────────┘
         ↓
    ┌──────────────────────────────────────────────┐
    │  hasMin && hasMax? (both present)             │
    │  YES: parse values, continue                 │
    │  NO: return hasConfig=false, allow            │
    └──────────────────────────────────────────────┘
         ↓
    ┌──────────────────────────────────────────────┐
    │  Create PDB with namespace label values       │
    │  (only if not already exists)                 │
    │  failurePolicy: Ignore → soft fail            │
    └──────────────────────────────────────────────┘
         ↓
         ↓
    ┌─────────────────────────────────────┐
    │   Validating Webhook (/validate)    │
    │   - Runs on CREATE and UPDATE        │
    │   - failurePolicy: Fail              │
    └─────────────────────────────────────┘
         ↓
    checkNamespaceConfig()
         ↓
    ┌──────────────────────────────────────────────┐
    │ Check: min label exists? max label exists?   │
    └──────────────────────────────────────────────┘
         ↓
    ┌─────────────────────────────────────────────────────────────────┐
    │  hasMin != hasMax?  (incomplete config)                          │
    │  YES: return error → REJECT deployment                           │
    │  NO: continue                                                    │
    └─────────────────────────────────────────────────────────────────┘
         ↓
    ┌──────────────────────────────────────────────┐
    │  hasMin && hasMax? (both present)             │
    │  YES: enforce PDB requirement, continue      │
    │  NO: no enforcement, allow deployment        │
    └──────────────────────────────────────────────┘
         ↓
    ┌──────────────────────────────────────────────┐
    │  hasPDB() - Check if matching PDB exists     │
    │  YES: allow deployment                       │
    │  NO: REJECT deployment                       │
    └──────────────────────────────────────────────┘
         ↓
    Deployment created (or rejected)
```

---

## Code Locations

### Mutating Webhook
**File**: `internal/handler/mutate.go`

1. **Handle()** (lines 35-142)
   - Entry point for mutation requests
   - Calls `getNamespacePDBLabels()` with new 4th return value (configErr)
   - If configErr is not empty: reject with error, return
   - If hasConfig=false: allow without mutation, return
   - If hasConfig=true: proceed to create PDB

2. **getNamespacePDBLabels()** (lines 219-272)
   - Reads namespace object
   - Checks both labels: `pdb-min-available` and `pdb-max-unavailable`
   - **Line 234**: If `hasMin != hasMax` → incomplete config
   - **Line 240**: Returns error if incomplete
   - **Line 244**: Returns `hasConfig=false` if neither exists
   - **Lines 251-262**: Parses both values and returns them

3. **createPDB()** (lines 180-217)
   - Creates PDB with:
     - selector from deployment spec
     - minAvailable if value > 0, else maxUnavailable
     - Labels identifying it as auto-created

### Validating Webhook
**File**: `internal/handler/validate.go`

1. **Handle()** (lines 31-120)
   - Entry point for validation requests
   - **Lines 91-100**: Calls `checkNamespaceConfig()`, rejects if configErr
   - **Lines 102-109**: Allows deployment if no config
   - **Lines 111-120**: Enforces PDB requirement if config present

2. **checkNamespaceConfig()** (lines 123-148)
   - Reads namespace object
   - Checks both labels
   - **Line 137**: If `hasMin != hasMax` → incomplete config
   - **Line 138**: Returns error if incomplete
   - **Line 142**: Returns `true` if both exist (enforce)
   - **Line 147**: Returns `false` if neither exist (no enforce)

3. **hasPDB()** (lines 150-195)
   - Lists all PDBs in namespace
   - For each PDB, checks if selector matches deployment's pod labels
   - Returns true if match found, false otherwise

---

## Label Values

Labels are set on the namespace:

```bash
kubectl label namespace production \
  pdb-min-available=2 \
  pdb-max-unavailable=1
```

### Parsing
- Both are converted from string to int
- Used to create `intstr.IntOrString` values
- Only **one** is used in PDB spec:
  - If `minAvailable > 0` → use minAvailable
  - Otherwise → use maxUnavailable

### Error Cases
- Invalid int value (e.g., "abc") → error returned
- Only one label present → error returned
- Both missing → no enforcement applied

---

## Webhook Configurations

### Mutating Webhook
- **Path**: `/mutate`
- **Operations**: CREATE only
- **failurePolicy**: Ignore (soft fail, deployment proceeds)
- **namespaceSelector**: None (runs on all namespaces, checks internally)

### Validating Webhook
- **Path**: `/validate`
- **Operations**: CREATE, UPDATE
- **failurePolicy**: Fail (strict, deployment rejected on error)
- **namespaceSelector**: None (runs on all namespaces, checks internally)

---

## Error Scenarios

### Scenario 1: Incomplete Config
```bash
kubectl label ns prod pdb-min-available=2
# Missing pdb-max-unavailable

kubectl apply -f deployment.yaml
# Error: incomplete PDB configuration: both pdb-min-available
# and pdb-max-unavailable labels must be set together
```

**Fix**: Add the missing label
```bash
kubectl label ns prod pdb-max-unavailable=1
```

### Scenario 2: No PDB with Enforcement
```bash
kubectl label ns prod pdb-min-available=2 pdb-max-unavailable=1
# Mutating webhook will auto-create PDB, OR:

kubectl apply -f deployment.yaml
# Error: deployment rejected: no PodDisruptionBudget in namespace prod
# selects pod labels app=myapp; create a PDB with a matching selector
```

### Scenario 3: Invalid Label Value
```bash
kubectl label ns prod pdb-min-available=abc
# Deployment rejected: invalid pdb-min-available value: abc
```

---

## Key Properties

1. **Idempotent**: Running webhook multiple times has same effect
2. **Graceful degradation**: Mutating webhook fails soft, validating webhook fails strict
3. **Namespace-scoped**: Each namespace has independent config
4. **Label-driven**: No global defaults, all config in namespace labels
5. **Paired enforcement**: Both labels must be present together
