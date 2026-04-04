# Webhook Semantic: Label-Based Enforcement

## Overview

The webhooks operate based on the presence of PDB configuration labels on namespaces:
- `pdb-min-available` (integer value)
- `pdb-max-unavailable` (integer value)

## Three Scenarios

### 1. Both Labels Present → Compulsory Mutation
If namespace has **both** `pdb-min-available` and `pdb-max-unavailable` labels:
- **Mutating Webhook**: Auto-creates a PDB for each Deployment if one doesn't exist
  - Uses the label values as min/max constraints
  - Only runs on CREATE operations
  - Fails gracefully (failurePolicy: Ignore)

- **Validating Webhook**: Enforces PDB exists
  - Rejects any Deployment without a matching PDB
  - Runs on CREATE and UPDATE operations
  - Fails strictly (failurePolicy: Fail)

**Example**:
```bash
kubectl label namespace production \
  pdb-min-available=2 \
  pdb-max-unavailable=1
```
Now deployments in `production` are auto-protected with PDBs.

---

### 2. Only One Label Present → Reject (Config Error)
If namespace has **only one** of the labels (min without max, or vice versa):
- **Mutating Webhook**: Rejects with error "incomplete PDB configuration"
- **Validating Webhook**: Rejects with error "incomplete PDB configuration"

This is an error condition. Users must set both labels together.

**Example** (ERROR):
```bash
# This is invalid - sets only min-available
kubectl label namespace broken pdb-min-available=2
# Deployments will be rejected
```

Fix it:
```bash
kubectl label namespace broken pdb-max-unavailable=1
# Now both are set, enforcement enabled
```

---

### 3. Neither Label Present → Allow (No Enforcement)
If namespace has **neither** label:
- **Mutating Webhook**: Skips mutation, allows deployment
- **Validating Webhook**: Skips enforcement, allows deployment

No constraints applied. Deployments can be created without PDBs.

**Example**:
```bash
# Namespace with no PDB labels
kubectl create namespace dev
# Deployments allowed freely, no PDB required
```

---

## Quick Reference

| Scenario | Min Label | Max Label | Mutating Webhook | Validating Webhook | Result |
|----------|-----------|-----------|------------------|-------------------|---------|
| Both present | Yes | Yes | Auto-create PDB | Enforce PDB exists | Deployment allowed (with PDB) |
| Only min | Yes | No | Reject (config error) | Reject (config error) | **Deployment REJECTED** |
| Only max | No | Yes | Reject (config error) | Reject (config error) | **Deployment REJECTED** |
| Neither | No | No | Skip | Skip | Deployment allowed (no PDB) |

---

## Implementation Details

### Mutating Webhook (`/mutate`)
1. Reads request namespace
2. Checks `pdb-min-available` and `pdb-max-unavailable` labels
3. If both exist: creates PDB with those values
4. If only one exists: logs error (not fatal, allows deployment)
5. If neither: allows deployment

### Validating Webhook (`/validate`)
1. Reads request namespace
2. Checks `pdb-min-available` and `pdb-max-unavailable` labels
3. If both exist: enforces matching PDB must exist (rejects if not)
4. If only one exists: rejects with config error
5. If neither: allows deployment

---

## Use Cases

### Strict Enforcement (Production)
```bash
kubectl label namespace prod \
  pdb-min-available=2 \
  pdb-max-unavailable=1
```
All deployments must have PDBs. Auto-created by mutating webhook.

### Testing/Dev (No Enforcement)
```bash
kubectl create namespace dev
# No labels - deployments allowed without PDBs
```

### Migration (Config Error Detection)
```bash
# Partially labeled namespace (incomplete)
kubectl label namespace staging pdb-min-available=2
# Deployments will be rejected - forces fixing config
# To fix: add the missing label
kubectl label namespace staging pdb-max-unavailable=1
```

---

## Error Messages

### Incomplete Configuration
```
deployment rejected: incomplete PDB configuration: both pdb-min-available
and pdb-max-unavailable labels must be set together
```

### Invalid Label Value
```
invalid pdb-min-available value: not-a-number
```

### No PDB Found (when enforcement enabled)
```
deployment rejected: no PodDisruptionBudget in namespace prod
selects pod labels app=nginx; create a PDB with a matching selector
before deploying
```
