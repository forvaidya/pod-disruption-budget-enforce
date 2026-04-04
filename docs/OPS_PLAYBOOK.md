# Operational Playbook

Common scenarios and how to handle them operationally.

## Scenario 1: Adding PDB Enforcement to Existing Namespace

**Situation:** Namespace has 5 existing deployments with no PDBs. You want to enable PDB enforcement.

**Steps:**

1. **Check current state**
   ```bash
   NAMESPACE=production
   kubectl get deployments -n $NAMESPACE
   kubectl get pdb -n $NAMESPACE
   ```

2. **Add labels to namespace**
   ```bash
   kubectl label namespace $NAMESPACE \
     pdb-min-available=1 \
     pdb-max-unavailable=1
   # Labels are now IMMUTABLE - cannot be removed or changed
   ```

3. **Monitor controller action**
   ```bash
   # Watch controller logs
   kubectl logs -n webhook-system deployment/pdb-webhook -f | \
     grep "rolling restart"

   # Should see: "triggering rolling restart for deployment" ×5
   ```

4. **Verify rolling restarts complete**
   ```bash
   # Watch pod replacements
   kubectl get pods -n $NAMESPACE -w

   # Pod template should have new annotation:
   kubectl get deployment app-server -n $NAMESPACE -o yaml | \
     grep -A5 "restartedAt"
   ```

5. **Verify PDBs created**
   ```bash
   # Should have 5 new PDBs
   kubectl get pdb -n $NAMESPACE

   # Check labels on auto-created PDBs
   kubectl get pdb -n $NAMESPACE -L app.kubernetes.io/managed-by
   ```

6. **Test enforcement**
   ```bash
   # Try to create new deployment without PDB - should be rejected
   kubectl apply -f test/deployment-without-pdb.yaml -n $NAMESPACE
   # Expected: Error - "deployment rejected: no PodDisruptionBudget..."
   ```

**Expected Timeline:**
- T+0s: Labels added
- T+1s: Controller processes namespace
- T+2s: Rolling restarts triggered (each deployment)
- T+30s: New pods created, webhook fires, PDBs auto-created
- T+45s: All deployments healthy with PDBs

**Risks to monitor:**
- [ ] Controller pod is healthy
- [ ] Webhook is responding (<1s latency)
- [ ] Rolling restarts don't cause service disruption
- [ ] Check application logs for unexpected pod restarts

---

## Scenario 2: Troubleshooting Failed Rolling Restart

**Situation:** You added PDB labels but PDBs weren't created, and deployments weren't restarted.

**Diagnosis:**

1. **Check controller logs for errors**
   ```bash
   kubectl logs -n webhook-system deployment/pdb-webhook | \
     grep -i "error\|rolling restart"

   # Look for: "failed to process deployment" or "failed to patch"
   ```

2. **Check if namespace was processed**
   ```bash
   kubectl logs -n webhook-system deployment/pdb-webhook | \
     grep "processing namespace"

   # If not found, labels may not have both values
   kubectl get namespace production --show-labels
   ```

3. **Check pod template annotations**
   ```bash
   kubectl get deployment app-server -n production -o yaml | \
     grep -A10 "template:"

   # Should have: kubectl.kubernetes.io/restartedAt: <timestamp>
   # If missing, rolling restart didn't trigger
   ```

4. **Check controller pod status**
   ```bash
   kubectl get pod -n webhook-system -l app=pdb-webhook
   kubectl describe pod <pod-name> -n webhook-system

   # Check: Running state, no resource limits hit, no crashes
   ```

**Resolution:**

- **If controller logs show "failed to patch deployment":**
  - Check RBAC: ClusterRole has "patch" verb on deployments
  - Check webhook-system ServiceAccount has ClusterRoleBinding
  - Manual workaround: Patch manually
    ```bash
    kubectl patch deployment app-server -n production \
      -p '{"spec":{"template":{"metadata":{"annotations":{"kubectl.kubernetes.io/restartedAt":"'$(date -u +'%Y-%m-%dT%H:%M:%SZ')'"}}}}}'
    ```

- **If controller logs show "failed to list deployments":**
  - Check RBAC: ClusterRole has "list" verb on deployments
  - Check namespace exists
  - Retry after fixing RBAC: Delete controller pod to trigger reconciliation

- **If namespace not processed:**
  - Verify BOTH labels are present (write-once check)
  - Check if controller predicate is filtering correctly
  - Try removing and re-adding labels

---

## Scenario 3: Operator Tries to Change Label Value

**Situation:** Someone tries to reduce `pdb-min-available` from 1 to 0 to increase availability.

**What happens:**
```bash
# Operator runs:
kubectl label namespace production pdb-min-available=0 --overwrite

# Output:
# Error from server (Forbidden):
# "PDB configuration label pdb-min-available cannot be changed after being set"
```

**Webhook logs:**
```
action=reject
reason=label-immutable
label=pdb-min-available
oldValue=1
newValue=0
```

**What to tell them:**
- Labels are immutable by design (write-once semantics)
- Change requires: delete namespace + recreate (not practical)
- Alternative: Update individual PDB minAvailable values instead
  ```bash
  # This is allowed - not the label, but the actual PDB resource
  kubectl patch pdb app-server -n production \
    -p '{"spec":{"minAvailable":0}}'
  ```

**Why this design:**
- Prevents accidental changes affecting existing workloads
- Ensures consistency across namespace
- All workloads created after label has same expectations
- Audit trail: unchanged labels = consistent enforcement

---

## Scenario 4: Deployment Rejected - "No PodDisruptionBudget"

**Situation:** Developer tries to deploy but gets rejected.

**Cause diagnosis:**

1. **Check if namespace has labels**
   ```bash
   kubectl get namespace default --show-labels

   # Should show: pdb-min-available=X pdb-max-unavailable=Y
   # If missing: No enforcement (deployment should be allowed)
   # If only one present: Config error (deployment rejected with error)
   ```

2. **Check if matching PDB exists**
   ```bash
   # Get pod labels from deployment
   kubectl get deployment app -n default -o yaml | grep -A5 "labels:"

   # List all PDBs
   kubectl get pdb -n default -o yaml

   # Check if any PDB selector matches pod labels
   ```

3. **Check webhook logs**
   ```bash
   kubectl logs -n webhook-system deployment/pdb-webhook | \
     grep "deployment rejected" | \
     grep "app" | \
     grep "default"
   ```

**Resolution:**

- **Missing PDB entirely:**
  - Create PDB with matching selector
  - Or: Use mutating webhook feature
    ```bash
    # Create deployment - mutating webhook auto-creates PDB
    # (only on CREATE, not UPDATE)
    kubectl apply -f deployment.yaml
    ```

- **PDB exists but selector doesn't match:**
  - Check pod template labels match PDB selector
  - Example fix: Add missing label to deployment
    ```bash
    kubectl patch deployment app -n default \
      -p '{"spec":{"template":{"metadata":{"labels":{"app":"app"}}}}}'
    ```

- **Partial namespace config (only one label present):**
  - Add missing label
  - This will trigger rolling restart (if other label was already present)

---

## Scenario 5: Webhook Unavailable - Deployments Failing

**Situation:** Webhook pod crashed. Deployments in enforced namespaces cannot be created.

**Severity:** HIGH - Webhook has `failurePolicy: Fail` (blocks requests on unavailability)

**Response:**

1. **Immediate: Check webhook status**
   ```bash
   kubectl get pod -n webhook-system -l app=pdb-webhook
   kubectl describe pod <pod-name> -n webhook-system
   ```

2. **Restore webhook**
   ```bash
   # Restart webhook pod
   kubectl delete pod <pod-name> -n webhook-system

   # Or increase replicas if all are down
   kubectl scale deployment pdb-webhook -n webhook-system --replicas=2
   ```

3. **Monitor recovery**
   ```bash
   # Watch until webhook pod is ready
   kubectl get pod -n webhook-system -w

   # Test webhook endpoint
   kubectl port-forward -n webhook-system svc/pdb-webhook 8443:443 &
   curl -k https://localhost:8443/healthz
   ```

4. **Retry blocked deployments**
   ```bash
   # Once webhook is healthy, developers can retry their deployments
   kubectl apply -f deployment.yaml
   ```

**Prevent future incidents:**
- [ ] Set webhook replicas ≥ 2
- [ ] Add PDB for webhook itself (to survive node drains)
- [ ] Monitor webhook pod health
- [ ] Alert on webhook CrashLoopBackOff
- [ ] Set timeoutSeconds to reasonable value (current: 10s)

---

## Scenario 6: Audit Review - Compliance Check

**Situation:** Compliance team wants to audit PDB enforcement across cluster.

**Steps:**

1. **Find all enforced namespaces**
   ```bash
   kubectl get ns -L pdb-min-available,pdb-max-unavailable | \
     grep -v "^default" | \
     grep -E "<none>|[0-9]"
   ```

2. **Check enforcement coverage**
   ```bash
   for ns in $(kubectl get ns -o name | cut -d/ -f2); do
     pdb_count=$(kubectl get pdb -n $ns --no-headers 2>/dev/null | wc -l)
     deploy_count=$(kubectl get deployment -n $ns --no-headers 2>/dev/null | wc -l)
     sts_count=$(kubectl get sts -n $ns --no-headers 2>/dev/null | wc -l)

     echo "$ns: PDBs=$pdb_count, Deployments=$deploy_count, StatefulSets=$sts_count"
   done
   ```

3. **Find PDBs created by webhook**
   ```bash
   # All auto-created PDBs
   kubectl get pdb -A -l app.kubernetes.io/managed-by=pdb-webhook-mutator

   # Count by namespace
   kubectl get pdb -A -l app.kubernetes.io/managed-by=pdb-webhook-mutator \
     -o custom-columns=NAMESPACE:.metadata.namespace,NAME:.metadata.name | \
     cut -d' ' -f1 | sort | uniq -c
   ```

4. **Check webhook rejection history**
   ```bash
   # Look for rejected deployments in past 7 days
   kubectl logs -n webhook-system deployment/pdb-webhook \
     --since=7d | \
     grep 'action=reject' | \
     wc -l

   # Breakdown by reason
   kubectl logs -n webhook-system deployment/pdb-webhook \
     --since=7d | \
     grep 'action=reject' | \
     grep -o 'reason=[^[:space:]]*' | \
     sort | uniq -c
   ```

5. **Audit trail report**
   ```bash
   # All PDB enforcement events in past 30 days
   kubectl logs -n webhook-system deployment/pdb-webhook \
     --since=30d | \
     grep -E 'action=(reject|allow|rolling-restart|skip)'
   ```

---

## Log Filtering Quick Reference

```bash
# All rejections
kubectl logs -n webhook-system deployment/pdb-webhook | grep 'action=reject'

# Rolling restarts triggered
kubectl logs -n webhook-system deployment/pdb-webhook | grep 'rolling-restart'

# PDBs auto-created
kubectl logs -n webhook-system deployment/pdb-webhook | grep 'PDB created'

# Label removal attempts
kubectl logs -n webhook-system deployment/pdb-webhook | grep 'label-removal'

# Label modification attempts
kubectl logs -n webhook-system deployment/pdb-webhook | grep 'label-immutable'

# For specific namespace
kubectl logs -n webhook-system deployment/pdb-webhook | grep 'namespace=production'

# For specific deployment
kubectl logs -n webhook-system deployment/pdb-webhook | grep 'name=app-server'
```

---

## Monitoring & Alerting Recommendations

### Key Metrics to Monitor

1. **Webhook Health**
   - Pod restarts > 0 in last hour → Alert
   - Webhook pod not Ready → Alert
   - Webhook latency > 5s → Alert

2. **Rejections**
   - action=reject count increasing → Investigate
   - reason=no-matching-pdb → PDBs not created
   - reason=label-removal/immutable → User education

3. **Controller Actions**
   - action=rolling-restart → Normal during label addition
   - failed to patch → RBAC or controller issue

### Alert Examples

```yaml
# Alert: Webhook pod down
expr: kube_pod_status_ready{pod=~"pdb-webhook.*"} == 0

# Alert: Too many rejections
expr: rate(webhooks_rejections_total[5m]) > 0.1

# Alert: PDB not created after rolling restart
# (Check if PDBs exist for deployments with recent restartedAt annotation)
```

