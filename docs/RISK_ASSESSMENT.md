# Risk Assessment & Mitigation

Operational risks when using PDB webhook enforcement.

## Risk Matrix

| Risk | Severity | Probability | Mitigation | Auditable |
|------|----------|-------------|-----------|-----------|
| Webhook unavailable - blocks all deployments in enforced namespaces | CRITICAL | Low | 2+ replicas, PDB for webhook, health checks | ✓ Yes |
| Label accidentally removed - enforcement stops | HIGH | Low | Immutable labels, webhook validation | ✓ Yes |
| Label value changed - inconsistent enforcement | HIGH | Low | Immutable labels, webhook validation | ✓ Yes |
| Rolling restart causes outage | MEDIUM | Medium | Coordinate with team, use disruption budgets | ✓ Yes |
| PDB auto-creation fails silently | MEDIUM | Low | Check controller logs, verify PDB exists | ✓ Yes |
| Deployment rejected - unclear error message | LOW | Medium | Clear error messages in webhook logs | ✓ Yes |
| Partial label configuration left | MEDIUM | Medium | Webhook rejects with config error | ✓ Yes |

---

## Critical Risks & Mitigation

### Risk 1: Webhook Unavailability

**Description:**
Webhook has `failurePolicy: Fail` - if webhook is unavailable, API server rejects ALL requests to resources covered by the webhook (Deployments, StatefulSets, Pods, Namespaces).

**Impact:**
- No new deployments can be created in ANY namespace
- Cannot update existing deployments
- Cannot update namespace labels
- Cluster unable to auto-scale
- Cannot apply critical updates

**Probability:** Low (modern k8s has good webhook error handling)

**Mitigation:**

1. **Redundancy**
   ```yaml
   spec:
     replicas: 2  # or 3 for production
   ```

2. **Pod Disruption Budget for webhook itself**
   ```yaml
   apiVersion: policy/v1
   kind: PodDisruptionBudget
   metadata:
     name: pdb-webhook
     namespace: webhook-system
   spec:
     minAvailable: 1
     selector:
       matchLabels:
         app: pdb-webhook
   ```

3. **Resource limits (prevent eviction)**
   ```yaml
   resources:
     requests:
       cpu: 100m
       memory: 128Mi
     limits:
       cpu: 500m
       memory: 512Mi
   ```

4. **Health check monitoring**
   - Monitor webhook pod Ready status
   - Alert on CrashLoopBackOff
   - Alert on resource quota exceeded

5. **Recovery procedure**
   - kubectl delete pod (triggers restart)
   - kubectl scale up replicas
   - Test with: `curl https://webhook:8443/healthz`

**Audit Trail:**
- Webhook pod restart times in kubectl events
- Failed deployments in webhook logs (would show request rejection)
- Controller logs would show reduced activity

---

### Risk 2: Label Removal/Modification Undetected

**Description:**
Someone removes or modifies PDB config labels on a namespace, causing enforcement to silently stop. Existing deployments continue working, but new deployments aren't checked.

**Impact:**
- New deployments created without PDBs
- No visibility into the change (no webhook alert since it's Namespace update)
- Inconsistency: some deployments have PDBs, others don't

**Probability:** Low (immutable labels + webhook validation prevents this)

**Why This Can't Happen:**
- Webhook validates all Namespace UPDATE operations
- Removes (missing labels) → Rejected with "cannot be removed" error
- Modifications (changed values) → Rejected with "cannot be changed" error
- Audit log shows rejection reason

**Detection:**
```bash
# Any removal/modification attempt shows in logs
kubectl logs -n webhook-system deployment/pdb-webhook | \
  grep -E 'label-removal|label-immutable'

# Will show: namespace=X, action=reject, reason=...
```

**What's NOT Prevented:**
- Adding additional labels (allowed)
- Removing unrelated labels (allowed)
- Only pdb-min-available and pdb-max-unavailable are protected

---

### Risk 3: Rolling Restart Causes Outage

**Description:**
When PDB labels are added to a namespace with existing deployments, the controller triggers rolling restarts. If coordination is poor, this could cause service disruption.

**Impact:**
- All pods in deployment restarted simultaneously (or in waves)
- Brief unavailability if no PDB protection initially
- Startup latency adds up

**Probability:** Medium (depends on team coordination)

**Mitigation:**

1. **Coordinate label addition with team**
   - Notify deployments team before adding labels
   - Schedule during maintenance window
   - Have runbook ready

2. **Use existing PDBs as safety**
   - If deployment already has a PDB, no restart triggered
   - Restart only triggered if PDB is missing
   - Add PDBs manually first, then add labels (zero impact)

3. **Gradual rollout**
   - Add labels to non-critical namespaces first
   - Test rolling restart behavior
   - Then roll out to critical namespaces

4. **Monitor during rollout**
   ```bash
   # Watch rolling restart progress
   kubectl rollout status deployment app-server -n production --timeout=5m
   kubectl get pods -n production -w
   ```

**Audit Trail:**
```
level=info msg="triggering rolling restart for deployment"
  deployment=app-server
  namespace=production
  action=rolling-restart
  reason=no-matching-pdb

# Later:
level=info msg="PDB created successfully"
  pdb=app-server
  namespace=production
```

---

### Risk 4: PDB Auto-Creation Fails

**Description:**
Mutating webhook attempts to create PDB during rolling restart (from pod recreation), but creation fails. Deployment ends up without PDB.

**Impact:**
- Validating webhook would reject UPDATE attempts on the deployment
- Deployment stuck in non-deterministic state
- Manual PDB creation needed

**Probability:** Very Low (webhook runs synchronously)

**Mitigation:**

1. **Check RBAC**
   - ClusterRole has "create" permission on PDBs
   - Verify with: `kubectl auth can-i create pdb --as=system:serviceaccount:webhook-system:pdb-webhook`

2. **Check for errors**
   ```bash
   kubectl logs -n webhook-system deployment/pdb-webhook | \
     grep -i "failed to create pdb"
   ```

3. **Manual recovery**
   ```bash
   # If PDB missing, create manually
   kubectl apply -f - <<EOF
   apiVersion: policy/v1
   kind: PodDisruptionBudget
   metadata:
     name: app-server
     namespace: production
   spec:
     minAvailable: 1
     selector:
       matchLabels:
         app: app-server
   EOF
   ```

**Audit Trail:**
- Webhook logs show "PDB created successfully" or error
- Resource creation timestamp shows when PDB was created

---

## Operational Risks

### Risk 5: Unclear Error Messages

**Description:**
Developers get rejection errors but don't understand why.

**Impact:**
- Development slowdown
- Support tickets
- Frustrated team

**Mitigation:**

1. **Clear error messages**
   - Webhook returns human-readable messages
   - Explains what's missing (PDB, labels, etc.)

2. **Documentation**
   - Ops playbook with common scenarios
   - Audit guide showing where to check
   - Example error messages with solutions

3. **Self-service**
   - Developers can see webhook logs
   - Teams can check namespace label status
   - Clear "what to do" in error message

**Error Examples:**
```
# Missing PDB
"deployment rejected: no PodDisruptionBudget in namespace production
selects pod labels app=nginx; create a PDB with a matching selector
before deploying"

# Incomplete config
"namespace has incomplete PDB configuration: both pdb-min-available
and pdb-max-unavailable labels must be set together"

# Label immutable
"PDB configuration label pdb-min-available cannot be changed after
being set; remove and re-add to change values"
```

---

### Risk 6: Partial Label Configuration

**Description:**
Only one of the two labels (min or max) is present on namespace.

**Impact:**
- Deployments rejected with config error (not "no PDB")
- Enforcement doesn't activate
- Unclear state

**Mitigation:**

1. **Webhook validation**
   - Rejects workloads if labels are incomplete
   - Clear error: "both labels must be set together"
   - Prevents silent failures

2. **Detection**
   ```bash
   # Check for partial configs
   kubectl get ns -o json | jq -r '.items[] |
     select((.metadata.labels["pdb-min-available"] != null) or
            (.metadata.labels["pdb-max-unavailable"] != null)) |
     .metadata.name'
   ```

3. **Resolution**
   - Add both labels atomically
   - Or use kubectl patch (overwrites both at once)

---

## Scenario: Complete Audit Trail Example

**Timeline: Adding PDB enforcement to production namespace**

```
2026-04-04 09:00:00 [OPS] Admin adds labels to production namespace
  kubectl label namespace production pdb-min-available=1 pdb-max-unavailable=1

2026-04-04 09:00:01 [WEBHOOK] Namespace UPDATE received
  Log: action=allow (labels being added, no old values to prevent)

2026-04-04 09:00:02 [CONTROLLER] Namespace reconciliation triggered
  Log: msg="processing namespace with PDB config labels"
       namespace=production pdb-min-available=1 pdb-max-unavailable=1

2026-04-04 09:00:03 [CONTROLLER] Found 3 deployments without PDBs
  Log: msg="triggering rolling restart for deployment"
       deployment=app-api namespace=production action=rolling-restart

2026-04-04 09:00:15 [WEBHOOK] New pods from rolling restart (old pods terminating)
  Log: msg="PDB created successfully"
       pdb=app-api namespace=production minAvailable=1

2026-04-04 09:00:45 [DEV] Developer tries new deployment
  API Request: Create Deployment app-new in production
  Webhook logs: msg="workload allowed by PDB"
                (PDB selector matches, or mutating webhook creates it)

2026-04-04 09:01:00 [DEV] Deployment succeeded ✓
```

**What's auditable from this timeline:**
1. ✓ When labels were added (kubectl describe ns shows time)
2. ✓ Which deployments were affected (controller logs)
3. ✓ When rolling restarts happened (pod creation times)
4. ✓ When PDBs were created (webhook logs "PDB created")
5. ✓ What enforcement is active (namespace labels)
6. ✓ Any rejections or errors (webhook logs with action=reject)

**If something went wrong:**
- Check controller logs: rolling restart failed?
- Check webhook logs: PDB creation failed?
- Check RBAC: controller can't patch deployments?
- Check webhook health: response time too high?

---

## Security Considerations

### Webhook RBAC

**Current permissions required:**
```yaml
# Read/List/Watch
- policy: PodDisruptionBudgets (read existing)
- "": Namespaces (read for label checks, watch for controller)
- apps: Deployments, StatefulSets (list for controller)

# Write
- policy: PodDisruptionBudgets (create for mutation)
- apps: Deployments, StatefulSets (patch for rolling restart)
```

**Risk:** Webhook can patch any deployment
**Mitigation:**
- Webhook runs in dedicated namespace (webhook-system)
- ServiceAccount is namespaced
- Consider namespace-specific webhook if needed

### Audit Log Sensitivity

**What's logged:**
- Deployment/Pod names (non-sensitive)
- Namespace names (non-sensitive)
- PDB configuration values (non-sensitive)
- Rejection reasons (non-sensitive)

**What's NOT logged:**
- Full pod specs (limited to labels only)
- Container images
- Secrets or environment variables
- Resource requests/limits

**Audit Trail Retention:**
- Webhook logs typically retained 7-30 days
- Use log aggregation (ELK, CloudLogging) for longer retention
- Webhook logs are standard zap/JSON format

---

## Compliance & Governance

### Audit Requirements Met

✓ **Immutability:** Labels cannot be removed or changed (write-once)
✓ **Traceability:** All critical operations logged with structured fields
✓ **Visibility:** Ops can query logs for who changed what, when
✓ **Preventive:** Webhook blocks invalid configurations
✓ **Detective:** Clear logs show rejections, creations, and controller actions

### Recommended Audit Policy

```yaml
# Kubernetes audit policy: log all admission webhook requests
- level: RequestResponse
  omitStages:
  - RequestReceived
  resources:
  - group: apps
    resources: ["deployments", "statefulsets", "pods"]
  - group: ""
    resources: ["namespaces"]
  - group: policy
    resources: ["poddisruptionbudgets"]
```

This captures all webhook decisions and resource modifications.

