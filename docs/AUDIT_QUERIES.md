# Audit & Observability Queries

Ready-to-use commands for operators to audit the system and understand what's happening.

## Quick Status Checks

### Check Webhook Health
```bash
# Is webhook running?
kubectl get pod -n webhook-system -l app=pdb-webhook

# Is webhook ready?
kubectl get deployment -n webhook-system pdb-webhook

# Test webhook endpoint
kubectl port-forward -n webhook-system svc/pdb-webhook 8443:443 &
curl -k https://localhost:8443/healthz
```

### Check Enforcement Status
```bash
# List all namespaces with PDB enforcement
kubectl get ns -L pdb-min-available,pdb-max-unavailable | grep -v "<none>"

# Show enforcement details
kubectl get ns -o wide \
  -o custom-columns=NAME:.metadata.name,MIN:.metadata.labels.pdb-min-available,MAX:.metadata.labels.pdb-max-unavailable

# For specific namespace
kubectl get namespace production --show-labels
```

### Check PDB Coverage
```bash
# Count PDBs per namespace
kubectl get pdb -A -o custom-columns=NAMESPACE:.metadata.namespace,NAME:.metadata.name | \
  cut -d' ' -f1 | sort | uniq -c

# Find PDBs created by webhook (auto-created)
kubectl get pdb -A -l app.kubernetes.io/managed-by=pdb-webhook-mutator

# Find PDBs created manually (not by webhook)
kubectl get pdb -A -L app.kubernetes.io/managed-by
```

---

## Audit Queries

### View Recent Label Changes
```bash
# All Namespace updates in past 24 hours
kubectl get events -A --field-selector involvedObject.kind=Namespace --sort-by='.lastTimestamp' | tail -20

# Or check directly - labels are immutable, so no changes should occur
# (If you see webhook rejections, someone tried to change labels)
```

### Find All Rejections
```bash
# Count rejections in last 24 hours
kubectl logs -n webhook-system deployment/pdb-webhook --since=24h | \
  grep -c 'action=reject'

# See rejection details
kubectl logs -n webhook-system deployment/pdb-webhook --since=24h | \
  grep 'action=reject'

# Group rejections by reason
kubectl logs -n webhook-system deployment/pdb-webhook --since=24h | \
  grep 'action=reject' | \
  grep -o 'reason=[^[:space:]]*' | sort | uniq -c

# Group rejections by namespace
kubectl logs -n webhook-system deployment/pdb-webhook --since=24h | \
  grep 'action=reject' | \
  grep -o 'namespace=[^[:space:]]*' | sort | uniq -c
```

### Track Controller Actions
```bash
# Count rolling restarts triggered
kubectl logs -n webhook-system deployment/pdb-webhook --since=24h | \
  grep -c 'action=rolling-restart'

# See which deployments were restarted
kubectl logs -n webhook-system deployment/pdb-webhook --since=24h | \
  grep 'action=rolling-restart'

# Count PDBs auto-created
kubectl logs -n webhook-system deployment/pdb-webhook --since=24h | \
  grep -c 'action="PDB created"'

# Details of each PDB creation
kubectl logs -n webhook-system deployment/pdb-webhook --since=24h | \
  grep 'PDB created successfully'
```

### Find Label Immutability Violations
```bash
# Someone tried to remove labels
kubectl logs -n webhook-system deployment/pdb-webhook --since=7d | \
  grep 'reason=label-removal-attempted'

# Someone tried to modify label values
kubectl logs -n webhook-system deployment/pdb-webhook --since=7d | \
  grep 'reason=label-immutable'

# Show who tried what (with timestamp and namespace)
kubectl logs -n webhook-system deployment/pdb-webhook --since=7d | \
  grep -E 'label-removal|label-immutable' | \
  cut -d' ' -f1,6,8,11
```

---

## Detailed Audit Trails

### For Specific Namespace

```bash
NAMESPACE=production

# 1. Check enforcement status
kubectl get namespace $NAMESPACE --show-labels

# 2. Check PDBs in namespace
kubectl get pdb -n $NAMESPACE

# 3. Check if any deployments don't have matching PDB
kubectl get deployment -n $NAMESPACE -o custom-columns=NAME:.metadata.name,LABELS:.spec.template.metadata.labels

# 4. Get all webhook events for this namespace
kubectl logs -n webhook-system deployment/pdb-webhook | \
  grep "namespace=$NAMESPACE"

# 5. Get rolling restart events
kubectl logs -n webhook-system deployment/pdb-webhook | \
  grep "namespace=$NAMESPACE" | \
  grep "action=rolling-restart"

# 6. Get rejection events for this namespace
kubectl logs -n webhook-system deployment/pdb-webhook | \
  grep "namespace=$NAMESPACE" | \
  grep "action=reject"
```

### For Specific Deployment

```bash
DEPLOYMENT=app-server
NAMESPACE=production

# 1. Check pod labels
kubectl get deployment $DEPLOYMENT -n $NAMESPACE -o yaml | \
  grep -A 10 "labels:"

# 2. Check if PDB exists
kubectl get pdb -n $NAMESPACE -o yaml | \
  grep -B5 "$DEPLOYMENT"

# 3. Check pod template annotation (rolling restart marker)
kubectl get deployment $DEPLOYMENT -n $NAMESPACE -o yaml | \
  grep -A5 "restartedAt"

# 4. Get webhook logs for this deployment
kubectl logs -n webhook-system deployment/pdb-webhook | \
  grep "name=$DEPLOYMENT" | \
  grep "namespace=$NAMESPACE"

# 5. Check pod events (last restart time)
kubectl get pods -n $NAMESPACE -l app=$DEPLOYMENT \
  -o custom-columns=NAME:.metadata.name,CREATED:.metadata.creationTimestamp,RESTARTS:.status.containerStatuses[0].restartCount
```

---

## Monitoring Queries

### Webhook Health Metrics

```bash
# Error rate (rejections / total requests)
kubectl logs -n webhook-system deployment/pdb-webhook --since=1h | \
  grep -c 'action=reject'
# vs
kubectl logs -n webhook-system deployment/pdb-webhook --since=1h | \
  grep -c 'action=allow'

# Latency (from log timestamps)
kubectl logs -n webhook-system deployment/pdb-webhook --tail=100 | \
  head -1  # first log line
kubectl logs -n webhook-system deployment/pdb-webhook --tail=1   # last log line
# Compare timestamps to estimate throughput

# Pod restart count
kubectl get pod -n webhook-system -l app=pdb-webhook \
  -o custom-columns=NAME:.metadata.name,RESTARTS:.status.containerStatuses[0].restartCount
```

### Enforcement Metrics

```bash
# Deployments per namespace with enforcement
for ns in $(kubectl get ns -o name | cut -d/ -f2); do
  min=$(kubectl get ns $ns -o jsonpath='{.metadata.labels.pdb-min-available}')
  max=$(kubectl get ns $ns -o jsonpath='{.metadata.labels.pdb-max-unavailable}')
  if [ -n "$min" ] && [ -n "$max" ]; then
    deploy=$(kubectl get deployment -n $ns --no-headers 2>/dev/null | wc -l)
    sts=$(kubectl get sts -n $ns --no-headers 2>/dev/null | wc -l)
    pdb=$(kubectl get pdb -n $ns --no-headers 2>/dev/null | wc -l)
    echo "$ns: min=$min max=$max deployments=$deploy statefulsets=$sts pdbs=$pdb"
  fi
done

# Coverage: Do all deployments in enforced namespace have PDB?
NAMESPACE=production
for deployment in $(kubectl get deployment -n $NAMESPACE -o name | cut -d/ -f2); do
  labels=$(kubectl get deployment $deployment -n $NAMESPACE -o jsonpath='{.spec.template.metadata.labels}')
  pdb=$(kubectl get pdb -n $NAMESPACE -o yaml | grep -c "matchLabels:" 2>/dev/null || echo "0")
  if [ "$pdb" -eq 0 ]; then
    echo "⚠️  $deployment has no matching PDB!"
  fi
done
```

---

## Time-Series Analysis

### Changes Over Time

```bash
# Track when enforcement was added to each namespace
# (Look for first occurrence of "processing namespace with PDB config labels")
kubectl logs -n webhook-system deployment/pdb-webhook --all-containers=true | \
  grep "processing namespace" | \
  awk '{print $2, $3, $6}' | sort

# Track PDB creation rate over time
kubectl logs -n webhook-system deployment/pdb-webhook --all-containers=true | \
  grep "PDB created" | \
  awk '{print $2, $3}' | uniq -c

# Track rejection rate over time
kubectl logs -n webhook-system deployment/pdb-webhook --all-containers=true | \
  grep "action=reject" | \
  awk '{print $2}' | sort | uniq -c
```

### Before/After Enforcement

```bash
# Before labels added (count deployments without PDB)
NAMESPACE=production
for deployment in $(kubectl get deployment -n $NAMESPACE -o name 2>/dev/null | cut -d/ -f2); do
  pdb=$(kubectl get pdb -n $NAMESPACE -o jsonpath="{.items[?(@.spec.selector.matchLabels.app=='$deployment')]}")
  if [ -z "$pdb" ]; then
    echo "No PDB: $deployment"
  fi
done

# After labels added (check all have PDB)
kubectl get deployment -n $NAMESPACE && \
kubectl get pdb -n $NAMESPACE
```

---

## Troubleshooting Queries

### Diagnose Deployment Rejection

```bash
# Recent rejections
kubectl logs -n webhook-system deployment/pdb-webhook --tail=100 | \
  grep 'action=reject'

# Specific reason
kubectl logs -n webhook-system deployment/pdb-webhook | \
  grep 'action=reject' | \
  grep 'deployment rejected'

# Which namespace?
kubectl logs -n webhook-system deployment/pdb-webhook | \
  grep 'action=reject' | \
  grep -o 'namespace=[^[:space:]]*'

# What labels does the deployment have?
kubectl get deployment app-server -n production -o yaml | \
  grep -A5 'labels:'
```

### Diagnose Missing PDB

```bash
# What should the PDB selector be?
kubectl get deployment app-server -n production -o jsonpath='{.spec.selector}'

# Check all PDBs in namespace
kubectl get pdb -n production -o yaml | \
  grep -A10 'selector:'

# Manual check: does selector match pod labels?
# (Compare deployment.spec.selector with pdb.spec.selector)
```

### Diagnose Rolling Restart Issues

```bash
# Did controller try to patch?
kubectl logs -n webhook-system deployment/pdb-webhook | \
  grep "triggering rolling restart"

# Did pod template annotation get added?
kubectl get deployment app-server -n production -o yaml | \
  grep -A5 "restartedAt"

# Did RBAC allow the patch?
kubectl get clusterrolebinding -l app.kubernetes.io/name=pdb-webhook
kubectl get clusterrole pdb-webhook -o yaml | grep -A5 "deployments"
```

---

## Exporting for Compliance

### Generate Audit Report

```bash
# Export all webhook actions in JSON format (for log analysis tools)
kubectl logs -n webhook-system deployment/pdb-webhook --since=30d -o json | \
  jq -r 'split("\n") | .[] | select(. | length > 0) | fromjson' > webhook-audit-30d.json

# Export enforcement summary
{
  echo "NAMESPACE,MIN_AVAILABLE,MAX_UNAVAILABLE,PDB_COUNT,DEPLOYMENT_COUNT"
  kubectl get ns -o json | jq -r '.items[] |
    [.metadata.name,
     .metadata.labels."pdb-min-available"//"",
     .metadata.labels."pdb-max-unavailable"//""] |
    @csv' | while IFS=, read ns min max; do
      if [ -n "$min" ] && [ -n "$max" ]; then
        pdb=$(kubectl get pdb -n "${ns//\"/}" --no-headers 2>/dev/null | wc -l)
        deploy=$(kubectl get deployment -n "${ns//\"/}" --no-headers 2>/dev/null | wc -l)
        echo "${ns//\"/},$min,$max,$pdb,$deploy"
      fi
    done
} | column -t -s,

# Export rejections
kubectl logs -n webhook-system deployment/pdb-webhook --since=30d | \
  grep 'action=reject' | \
  sed 's/.*msg="\([^"]*\)".*/\1/' | sort | uniq -c
```

---

## Example: Complete Audit Scenario

### Scenario: Auditing Namespace Configuration Change

```bash
#!/bin/bash
# Audit a specific namespace's PDB enforcement history

NAMESPACE=${1:-production}

echo "=== Namespace: $NAMESPACE ==="
echo ""

echo "1. Current Enforcement Status:"
kubectl get namespace $NAMESPACE --show-labels | grep pdb-

echo ""
echo "2. PDBs in Namespace:"
kubectl get pdb -n $NAMESPACE --no-headers

echo ""
echo "3. Deployments without PDB:"
for deployment in $(kubectl get deployment -n $NAMESPACE -o name | cut -d/ -f2); do
  echo "  - $deployment"
done

echo ""
echo "4. Recent Webhook Actions:"
kubectl logs -n webhook-system deployment/pdb-webhook --since=7d | \
  grep "namespace=$NAMESPACE" | tail -10

echo ""
echo "5. Label Change Attempts:"
kubectl logs -n webhook-system deployment/pdb-webhook --since=7d | \
  grep "namespace=$NAMESPACE" | grep -E 'label-removal|label-immutable' || echo "  None found ✓"

echo ""
echo "6. Rolling Restarts Triggered:"
kubectl logs -n webhook-system deployment/pdb-webhook --since=7d | \
  grep "namespace=$NAMESPACE" | grep "action=rolling-restart" || echo "  None found"

echo ""
echo "7. PDB Auto-Creations:"
kubectl logs -n webhook-system deployment/pdb-webhook --since=7d | \
  grep "namespace=$NAMESPACE" | grep "PDB created" || echo "  None found"
```

**Run it:**
```bash
./audit-namespace.sh production
```

**Output shows everything ops needs to know about what happened to that namespace.**

