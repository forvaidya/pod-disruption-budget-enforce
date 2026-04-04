# Capacity Planning: IP Space & PDB Cutover

## The Problem

During rolling updates/cutover:
- **Old deployment**: Running (still serving traffic)
- **New deployment**: Starting up
- **Overlap period**: Both running simultaneously
- **PDB constraint**: minAvailable must be satisfied during this

## IP Space Requirement

### Formula

```
Required IPs = (maxReplicas × 2) + minAvailable + buffer

Where:
  maxReplicas = Maximum pod count (HPA max)
  2 = Old deployment (being replaced) + New deployment (coming up)
  minAvailable = PDB minimum that must stay up
  buffer = Extra IPs for safety margin (usually 10-20%)
```

### Example Calculation

```
HPA Config:
  maxReplicas: 10

PDB Config:
  minAvailable: 2

During Cutover:
  Old Deployment: 10 pods (being replaced)
  New Deployment: 10 pods (being created)
  Temporary Peak: 20 pods
  PDB Requirement: minAvailable=2 must stay UP

IP Capacity Needed:
  = (10 × 2) + 2 + buffer
  = 20 + 2 + 2 (10% buffer)
  = 24 IPs minimum
```

## Real Scenarios

### Scenario 1: Small App
```
maxReplicas:    5
minAvailable:   1
PDB maxUnavailable: 2

During cutover:
  Old: 5 pods
  New: 5 pods
  Total: 10 pods
  Must keep: 1 minimum alive
  IP need: 10 + 1 + buffer = 12 IPs
```

### Scenario 2: Large App (Your Case)
```
maxReplicas:    10
minAvailable:   3
PDB maxUnavailable: 4

During cutover:
  Old: 10 pods
  New: 10 pods
  Total: 20 pods
  Must keep: 3 minimum alive
  IP need: 20 + 3 + buffer = 25+ IPs
```

### Scenario 3: Zero-Downtime (Extreme)
```
maxReplicas:    10
minAvailable:   10 (ALL pods must stay up)

During cutover:
  Old: 10 pods
  New: 10 pods
  Total: 20 pods
  Must keep: 10 minimum alive
  IP need: 20 + 10 + buffer = 32+ IPs
  
Implication:
  - RollingUpdate: maxSurge=10, maxUnavailable=0
  - Requires 2x IP space for all maxReplicas
```

## Checking Your IP Capacity

### Current Cluster IPs

```bash
# Check CNI plugin and IP range
kubectl get nodes -o wide

# Check pod CIDR and available IPs
kubectl cluster-info dump | grep -i cidr

# Calculate available IPs
# If pod CIDR is 10.0.0.0/16: 65,536 available IPs
# Minus system pods, margins, etc.: ~60,000 usable
```

### Calculate Usage

```bash
# Current pod count
kubectl get pods --all-namespaces | wc -l

# Per-namespace max
kubectl get deployment -A -o jsonpath='{.items[*].spec.replicas}' | \
  awk '{sum+=$1} END {print "Total replicas:", sum}'

# Check for resource exhaustion
kubectl describe nodes | grep -i "allocated\|capacity"
```

## Safety Margins

### Recommended Buffer

```
For Production:
  Buffer = 20-30% of required IPs

For High-Availability:
  Buffer = 30-50% of required IPs

Example:
  Calculated Need: 25 IPs
  With 25% buffer: 25 × 1.25 = 31 IPs
  Should have: ≥31 IPs available
```

## What Happens If You Run Out

```
During Cutover (IP exhaustion):
  Old pods: 10 (still terminating)
  New pods: Want to create 10, but NO IPs left
  
Result:
  ❌ New pods stuck in "Pending" (waiting for IP)
  ❌ Deployment hangs (can't replace old pods)
  ❌ Service degradation
  ❌ PDB violation possible (can't satisfy minAvailable)
```

## Prevention Checklist

- [ ] Calculate worst-case IP need (maxReplicas × 2)
- [ ] Add minAvailable requirement
- [ ] Add 20-30% safety buffer
- [ ] Verify cluster has this many available IPs
- [ ] Monitor IP usage: `kubectl describe nodes`
- [ ] Test rolling update on non-prod first
- [ ] Alert if IP usage > 80%
- [ ] Plan cluster growth before hitting limits

## PDB & IP Interaction

### Incompatible Configuration (Will Fail)

```yaml
PDB:
  minAvailable: 10  # All pods must stay up

HPA:
  maxReplicas: 10

Rolling Update:
  Old: 10 pods (terminating)
  New: 10 pods (want to start)
  Required: 20 IPs + 10 (minAvailable) = 30 IPs
  
  But PDB says: "Can't evict, need 10 up"
  And Rolling Update says: "Can't start new without evicting old"
  
  Result: ❌ DEADLOCK
```

### Safe Configuration

```yaml
PDB:
  minAvailable: 3        # Keep some up, but allow some eviction

HPA:
  maxReplicas: 10

Rolling Update:
  Old: 10 pods (can evict some)
  New: 10 pods (can start)
  Required: ~20 IPs (during peak)
  
  PDB allows: "Evict max 7 (10-3=7 can go)"
  Rolling Update: "Start new, evict old progressively"
  
  Result: ✅ Progressive replacement
```

## Cutover Procedure (Safe)

```bash
# Before cutover, check IP availability
kubectl describe nodes | grep "Allocated resources"

# Verify IP space is available
# If not, scale down other apps or expand cluster

# Check minAvailable will be met
kubectl get pdb -A

# Do gradual rollout
kubectl set image deployment/app \
  app=myapp:v2 \
  --record \
  --image-record

# Monitor during update
kubectl rollout status deployment/app
kubectl get pods -w  # Watch IP allocation

# Verify PDB constraints were met
kubectl get pdb app -o jsonpath='{.status}'
```

## Summary

| Metric | Your Config | During Cutover | Buffer | Total Needed |
|---|---|---|---|---|
| maxReplicas | 10 | Old: 10 + New: 10 = 20 | 20% | 24 |
| minAvailable | 3 | Must keep 3 UP | - | 3 |
| **Total IPs** | - | - | - | **27+** |

**Rule of Thumb:**
```
IPs Needed = (maxReplicas × 2) × 1.25
           = (10 × 2) × 1.25
           = 25 IPs minimum
```

