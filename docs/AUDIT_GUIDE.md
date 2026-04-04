# Audit & Visibility Guide

This guide helps operators audit PDB enforcement, track label changes, and identify risks.

## Key Audit Points

### 1. Namespace Label Configuration
**Where:** Namespace `pdb-min-available` and `pdb-max-unavailable` labels

```bash
# List all namespaces with PDB enforcement enabled
kubectl get ns -L pdb-min-available,pdb-max-unavailable

# Watch for label changes
kubectl get ns -w -L pdb-min-available,pdb-max-unavailable
```

**Audit Trail:**
- Labels are **immutable once set** (write-once semantics)
- Any attempt to remove or modify labels is **rejected** and logged
- Only initial addition is allowed

### 2. Webhook Logs - Validation Rejections

**Log Pattern:** Look for `rejecting` or `rejected` in webhook logs
```
level=info caller=validate.go:180 msg="workload rejected"
  kind=Deployment name=app-server namespace=production
  reason="deployment rejected: no PodDisruptionBudget..."
```

**What to audit:**
- Deployment/StatefulSet rejections → missing PDBs
- Bare Pod rejections → enforcement enabled in namespace
- Namespace update rejections → attempted label removal/modification

**Query logs:**
```bash
kubectl logs -n webhook-system deployment/pdb-webhook -f | grep rejected
```

### 3. Webhook Logs - PDB Auto-Creation

**Log Pattern:** Look for `PDB created` in webhook logs
```
level=info caller=mutate.go:211 msg="PDB created successfully"
  pdb=app-server namespace=production minAvailable=1
```

**What to audit:**
- Which workloads had PDBs auto-created
- When creation occurred
- What min/max values were used (from namespace labels)

**Query logs:**
```bash
kubectl logs -n webhook-system deployment/pdb-webhook -f | grep "PDB created"
```

### 4. Controller Logs - Namespace Config Activation

**When:** Labels are added to an existing namespace

**Log Pattern:** Look for `processing namespace` or `triggering rolling restart`
```
level=info caller=namespace_controller.go:50
  msg="processing namespace with PDB config labels" namespace=production

level=info caller=namespace_controller.go:94
  msg="triggering rolling restart for deployment"
  deployment=app-server namespace=production

level=info caller=namespace_controller.go:104
  msg="rolled out deployment" deployment=app-server namespace=production
```

**What to audit:**
- Which workloads got rolling restarts triggered
- When the restart occurred
- Pod template annotation change: `kubectl.kubernetes.io/restartedAt`

**Query logs:**
```bash
kubectl logs -n webhook-system deployment/pdb-webhook -f | grep "rolling restart"
kubectl logs -n webhook-system deployment/pdb-webhook -f | grep "rolled out"
```

### 5. Resource Labels for Audit

**PDBs created by webhook have labels:**
```yaml
metadata:
  labels:
    app.kubernetes.io/name: pdb-webhook
    app.kubernetes.io/component: admission-controller
    app.kubernetes.io/managed-by: pdb-webhook-mutator
    pdb-webhook.workload-name: <deployment-name>
```

**Query auto-created PDBs:**
```bash
# Find all PDBs created by the webhook
kubectl get pdb -A -l app.kubernetes.io/managed-by=pdb-webhook-mutator

# Find PDB for specific workload
kubectl get pdb -A -l pdb-webhook.workload-name=app-server
```

## Audit Checklist for Ops

### Daily/Weekly
- [ ] Check webhook logs for rejections (indicates missing PDBs)
- [ ] Verify webhook pod is healthy and available
- [ ] Check for any namespace update rejections (attempted label changes)

### When Adding Labels to Namespace
- [ ] Verify namespace has both labels set (not partial)
- [ ] Watch controller logs for rolling restart completion
- [ ] Confirm new PDBs are created for all workloads
- [ ] Check pod template annotations updated with `restartedAt`

### Risk Identification

**High Risk Scenarios:**
1. **Namespace with labels but no PDBs exist**
   - Check: `kubectl get pdb -n <namespace>`
   - Risk: New deployments will be rejected
   - Action: Ensure controller ran successfully, check logs

2. **Workload rejected with "no PodDisruptionBudget"**
   - Check: When was PDB config label added?
   - Check: Did controller trigger rolling restart?
   - Action: May need manual PDB creation if controller failed

3. **Partial label configuration (only one label present)**
   - Check: Which label is missing?
   - Risk: New workloads rejected with config error
   - Action: Add missing label (and provide value)

4. **Webhook unavailable**
   - Check: `kubectl get deployment pdb-webhook -n webhook-system`
   - Risk: New deployments in enforced namespaces cannot be created
   - Action: Check pod logs, restore webhook availability

## Log Search Examples

### Find all PDB enforcement activations (last 7 days)
```bash
kubectl logs -n webhook-system deployment/pdb-webhook \
  --since=7d | grep "processing namespace with PDB config"
```

### Find all rejected deployments
```bash
kubectl logs -n webhook-system deployment/pdb-webhook \
  --since=7d | grep "deployment rejected"
```

### Find all PDB auto-creations
```bash
kubectl logs -n webhook-system deployment/pdb-webhook \
  --since=7d | grep "PDB created"
```

### Find namespace label removal attempts
```bash
kubectl logs -n webhook-system deployment/pdb-webhook \
  --since=7d | grep "cannot be removed"
```

## Structured Logging Fields

All logs include structured fields for easy filtering:

```
- namespace: Target namespace
- kind: Resource kind (Deployment, StatefulSet, Pod, Namespace)
- name: Resource name
- pdb: PDB name (for creation logs)
- minAvailable: Min available value (from namespace label)
- maxUnavailable: Max unavailable value (from namespace label)
- reason: Rejection reason (for denied requests)
- oldValue/newValue: Label values (for modification attempts)
```

## Webhook Health Monitoring

### Check webhook readiness
```bash
kubectl get deployment pdb-webhook -n webhook-system
kubectl describe deployment pdb-webhook -n webhook-system
```

### Check webhook endpoint
```bash
kubectl port-forward -n webhook-system svc/pdb-webhook 8443:443
curl -k https://localhost:8443/healthz
```

### Monitor webhook latency
- Check `timeoutSeconds: 10` in ValidatingWebhookConfiguration
- Monitor webhook response times in logs
- Alert if webhook becomes slow (>1s per request)

## Example: Audit Event Timeline

**Day 1: Label addition**
```
2026-04-04 10:00:00 Admin adds pdb-min-available=1, pdb-max-unavailable=1 to production namespace
→ Log: "processing namespace with PDB config labels" namespace=production

2026-04-04 10:00:02 Controller finds 3 deployments without PDBs
→ Log: "triggering rolling restart for deployment" (×3)

2026-04-04 10:00:15 Rolling restarts complete, new pods created
→ Log: "PDB created successfully" (×3)
→ New PDBs visible: kubectl get pdb -n production

2026-04-04 10:15:00 New deployment created
→ Mutating webhook creates PDB automatically
→ Validating webhook allows deployment
→ Success!
```

**Day 5: Risk detected**
```
2026-04-09 15:30:00 Ops attempts to reduce pdb-min-available to 0
→ Log: "rejecting namespace update: pdb-min-available label value cannot be changed"
→ Update rejected (immutable)

2026-04-09 15:30:05 Ops creates new namespace without labels (to test)
→ Deployment allowed (no enforcement)
→ PDBs NOT auto-created
→ Ops must manually add labels if enforcement needed
```

## Summary

- **Audit Trail:** All critical operations logged with structured fields
- **Immutability:** Labels locked after initial set (prevents accidental changes)
- **Visibility:** Clear logs for rejections, creations, and controller actions
- **Risk Detection:** Structured logs enable easy identification of misconfiguration
