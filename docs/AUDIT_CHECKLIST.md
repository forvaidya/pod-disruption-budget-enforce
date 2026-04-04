# Audit Checklist for Operations

Quick reference for auditing PDB webhook enforcement across your cluster.

## Daily Checks

- [ ] **Webhook Health**
  ```bash
  kubectl get deployment -n webhook-system pdb-webhook
  # Should show: Ready 1/1 or higher, no crashes
  ```

- [ ] **Recent Rejections**
  ```bash
  kubectl logs -n webhook-system deployment/pdb-webhook --since=24h | grep 'action=reject' | wc -l
  # Review if count is higher than normal
  ```

- [ ] **Label Tampering Attempts**
  ```bash
  kubectl logs -n webhook-system deployment/pdb-webhook --since=24h | grep -E 'label-removal|label-immutable' | wc -l
  # Should be 0 (if >0, someone tried to change enforcement)
  ```

## Weekly Checks

- [ ] **Enforcement Coverage**
  ```bash
  # Count enforced vs non-enforced namespaces
  kubectl get ns -L pdb-min-available,pdb-max-unavailable
  ```

- [ ] **PDB Coverage**
  ```bash
  # For each enforced namespace, verify deployments have PDBs
  for ns in $(kubectl get ns -L pdb-min-available | grep -v "<none>" | awk '{print $1}'); do
    echo "Namespace: $ns"
    kubectl get deployment -n $ns --no-headers 2>/dev/null | wc -l
    kubectl get pdb -n $ns --no-headers 2>/dev/null | wc -l
  done
  ```

- [ ] **Recent Controller Actions**
  ```bash
  kubectl logs -n webhook-system deployment/pdb-webhook --since=7d | grep -E 'rolling-restart|PDB created' | wc -l
  # Only appears when labels are added or deployments redeployed
  ```

## Monthly Audit

- [ ] **Complete Enforcement Inventory**
  ```bash
  # Export all enforcement configurations
  kubectl get ns -o json | jq -r '.items[] |
    select(has("metadata") and .metadata.labels | select(.["pdb-min-available"] != null)) |
    [.metadata.name, .metadata.labels["pdb-min-available"], .metadata.labels["pdb-max-unavailable"]] | @csv'
  ```

- [ ] **Rejection Analysis**
  ```bash
  # Summarize rejections by reason
  kubectl logs -n webhook-system deployment/pdb-webhook --since=30d | \
    grep 'action=reject' | \
    grep -o 'reason=[^[:space:]]*' | sort | uniq -c
  ```

- [ ] **Label Change Attempts**
  ```bash
  # Should be zero (immutability prevents changes)
  kubectl logs -n webhook-system deployment/pdb-webhook --since=30d | \
    grep -E 'label-removal|label-immutable' | wc -l
  ```

- [ ] **Webhook Performance**
  ```bash
  # Check webhook pod health and restart count
  kubectl get pod -n webhook-system -l app=pdb-webhook \
    -o custom-columns=NAME:.metadata.name,RESTARTS:.status.containerStatuses[0].restartCount
  # RESTARTS should be 0 or very low
  ```

## Risk Assessment

After running above checks, answer:

1. **Is enforcement consistent?**
   - [ ] All enforced namespaces have both labels
   - [ ] No partial configurations (only one label)
   - [ ] No attempt to remove/modify labels

2. **Is coverage adequate?**
   - [ ] All deployments in enforced namespace have PDBs
   - [ ] Auto-created PDBs have correct selectors
   - [ ] No rejections for valid deployments

3. **Is webhook healthy?**
   - [ ] No pod crashes or restarts
   - [ ] Response latency acceptable (<1s)
   - [ ] High availability (2+ replicas)

4. **Are operations smooth?**
   - [ ] No unexpected rejections
   - [ ] Rolling restarts complete cleanly
   - [ ] Developers understand error messages

## Compliance Report

### What Can Be Audited
✓ Immutability: Labels locked after initial set (write-once)
✓ Traceability: All operations logged with structured fields
✓ Visibility: Clear logs show who changed what, when
✓ Preventive: Webhook blocks invalid configurations
✓ Detective: Logs show rejections, creations, actions

### What Ops Can Query
- [x] Which namespaces have enforcement enabled
- [x] Which deployments have auto-created PDBs
- [x] When PDB enforcement was activated
- [x] All rejected deployment attempts
- [x] All attempted label changes (blocked)
- [x] Rolling restart events and timing
- [x] Webhook pod health and uptime

### Sample Audit Report

```
=== PDB Enforcement Audit Report ===
Report Date: 2026-04-04
Period: Last 30 days

Enforcement Status:
  Total Namespaces: 10
  Enforced: 3 (production, staging, shared-services)
  Non-Enforced: 7

PDB Coverage:
  Total Deployments (enforced): 42
  With PDB: 42 (100%)
  Missing PDB: 0 (0%)

Webhook Activity (30 days):
  Total Requests: 2,847
  Allowed: 2,821 (99.1%)
  Rejected: 26 (0.9%)
    - Missing PDB: 18
    - Config incomplete: 5
    - Other: 3

Security Events (30 days):
  Label Removal Attempts: 0 ✓
  Label Modification Attempts: 0 ✓
  Unauthorized Access: 0 ✓

Webhook Health:
  Pod Restarts: 0
  Avg Response Time: 45ms
  Availability: 99.99%

Risks Identified:
  None - System operating as expected
```

## Automated Monitoring

### Prometheus Queries (if using monitoring)

```promql
# Webhook request rate
rate(admission_webhook_requests_total[5m])

# Rejection rate
rate(admission_webhook_rejections_total[5m])

# Pod restart count
increase(kube_pod_container_status_restarts_total{pod=~"pdb-webhook.*"}[1h])

# PDB coverage
count(kube_poddisruptionbudget_status_pods_available) / count(kube_deployment_status_replicas)
```

### Alert Rules

```yaml
- alert: WebhookDown
  expr: up{job="pdb-webhook"} == 0
  for: 5m
  severity: critical

- alert: WebhookHighErrorRate
  expr: rate(admission_webhook_rejections_total[5m]) > 0.1
  for: 10m
  severity: warning

- alert: WebhookHighLatency
  expr: histogram_quantile(0.99, admission_webhook_duration_seconds) > 1
  for: 5m
  severity: warning
```

## Quick Links

- **Audit Details**: [AUDIT_GUIDE.md](./AUDIT_GUIDE.md)
- **Operational Scenarios**: [OPS_PLAYBOOK.md](./OPS_PLAYBOOK.md)
- **Risk Analysis**: [RISK_ASSESSMENT.md](./RISK_ASSESSMENT.md)
- **Audit Queries**: [AUDIT_QUERIES.md](./AUDIT_QUERIES.md)
