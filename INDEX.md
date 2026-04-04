# PDB Admission Webhook - Operational Philosophy

## Why This Exists

### The Problem: EKS Upgrades Are Disruptive

AWS EKS clusters undergo regular upgrades:

1. **Control Plane Upgrade** (AWS-managed, ~30 minutes)
   - API server restarts
   - etcd updates
   - Minimal pod disruption, but still affects scheduling

2. **Worker Node Replacement** (You manage, 1-2 hours)
   - Old worker nodes cordoned (no new pods)
   - Existing pods must be evicted
   - **This is where things break** if pods aren't properly distributed

### The Risk Without PDB Enforcement

**Scenario: Uncontrolled Node Drain**
```
Worker Node (10 pods, 1 replica each)
  └─ pod-1 (app-a)
  └─ pod-2 (app-b)
  └─ pod-3 (app-c)
  └─ ... (7 more)

When node drains:
  ❌ All 10 pods evicted simultaneously
  ❌ No replacement pods scheduled (no capacity elsewhere)
  ❌ Service outages for 10 applications
  ❌ Thundering herd of deployments restarting
```

**Scenario: Gradual, Controlled Drain (WITH PDB)**
```
Worker Node (10 pods, 3 replicas each)
  ├─ pod-1 (app-a)
  ├─ pod-2 (app-b)
  └─ pod-3 (app-c)
  ... (7 more across redundant replicas)

When node drains:
  ✅ 1-2 pods evicted at a time
  ✅ Replacement pods scheduled immediately (PDB allows)
  ✅ Service continues with reduced capacity
  ✅ Gradual rebalancing, no thundering herd
  ✅ Cluster becomes more resilient
```

---

## What We're Enforcing

### 1. Graceful Pod Disruption

**PodDisruptionBudget (PDB):**
- Kubernetes tells nodes "how many pods can you evict?"
- Without PDB: node drains kill all pods immediately
- With PDB: node drains respect min/max pod availability

**Example:**
```yaml
PodDisruptionBudget:
  minAvailable: 2      # At least 2 replicas must be available
  maxUnavailable: 1    # At most 1 pod can be unavailable

Deployment: 3 replicas
  → During node drain: 2 stay, 1 gets evicted
  → Service continues with reduced capacity
  → New pod scheduled, capacity restored
```

---

### 2. Namespace-Based Discipline

Not all namespaces are equal:

| Namespace | Config | Meaning | Typical Use |
|---|---|---|---|
| `production` | `pdb-min-available=2` | **Enforced production** | Mission-critical services |
| `staging` | `pdb-min-available=1` | **Standard deployments** | Pre-production testing |
| `dev` | None | **Temporary deployments** | Development, testing, experiments |
| `experimental` | None | **Throwaway workloads** | Proof of concepts |

**Key Rule:** If a namespace doesn't have PDB constraints, it's considered **temporary**. If you need production-grade reliability, you must declare it with namespace labels.

---

### 3. IP Space Budgeting

Kubernetes clusters run on subnets with finite IP space:

```
Subnet: 10.0.0.0/22 (1024 IPs)
├─ Reserved: 4 IPs
├─ Nodes: 50 IPs (50 nodes)
└─ Pods: 970 IPs

Rolling Update Capacity Calculation:
  Replicas: 10
  Max Unavailable: 2

  Worst case during update:
    Old deployment: 10 pods (running)
    New deployment: 10 pods (deploying)
    Total: 20 IPs needed simultaneously

  Available: 970 IPs
  ✅ Fine
```

**Without Planning:**
```
Dev team deploys:
  - 100-replica service (assumed growth)
  - 50-replica cache layer
  - 40-replica message queue
  Total: 190 replicas

During rolling update:
  190 (old) + 190 (new) = 380 pods
  Need 380 IPs
  Have: 970 IPs
  Available: 590 IPs
  ❌ Fails: IP exhaustion
```

**Namespace labels provide budgeting:**
```
kubectl label ns production max-replicas-budget=200
```

This label signals: "We might scale to 200 replicas, plan IP space accordingly."

---

### 4. Team Discipline & Operational Visibility

Without constraints, teams drift:

```
❌ Without PDB enforcement:
  - Developer deploys to "prod" for quick testing
  - Service runs 1 replica (fast, cheap)
  - During EKS upgrade: service goes down
  - Postmortem: "We thought it was temporary"

✅ With PDB enforcement:
  - To deploy to "prod", must set pdb-min-available=N
  - Requires 2+ replicas
  - Requires understanding of capacity needs
  - Ops team can track: "This is truly production"
```

**Namespace labels become organizational signals:**
- `pdb-min-available=2` → "This service is important"
- No labels → "This is for testing/development"
- Labels match business criticality

---

## How This Webhook Enforces It

### Three-Tier Enforcement

#### Tier 1: Namespace Configuration (Labels)

```bash
# Strongly-typed constraints
kubectl label namespace production \
  pdb-min-available=2 \
  pdb-max-unavailable=1
```

**Why both labels?**
- `minAvailable`: "We need this many replicas UP" (reliability)
- `maxUnavailable`: "We can tolerate losing this many" (capacity planning)
- Both required: forces thinking about both dimensions

#### Tier 2: Automatic PDB Creation (Mutating Webhook)

```
Developer deploys service to "production" namespace
         ↓
Mutating webhook intercepts
         ↓
Checks: Does "production" namespace have both labels? YES
         ↓
Creates PDB automatically:
  minAvailable: (from label)
  Selector: matches deployment pods
         ↓
Deployment continues, now protected by PDB
```

**Benefit:** Zero friction for developers, automatic safety.

#### Tier 3: Enforcement (Validating Webhook)

```
Developer tries to update deployment
         ↓
Validating webhook intercepts
         ↓
Checks: Does a matching PDB exist?
         ↓
YES  → Allow update
NO   → Reject with: "Create a PDB or disable constraints"
```

**Benefit:** If PDB is accidentally deleted, system enforces recreation.

---

## The Operational Model

### Before (Chaos)

```
EKS Cluster During Node Upgrade

Production Apps (1 replica each):
  ├─ Payment Service (critical)
  ├─ Auth Service (critical)
  ├─ Search Service
  ├─ Analytics Service
  ├─ Monitoring Service
  └─ 10+ others

Node drains:
  All 16 pods evicted simultaneously

Result:
  🔴 Payment service down
  🔴 Auth service down
  🔴 Cascading failures
  🔴 30-minute incident
  🔴 Postmortem meetings
  🔴 Customer SLA violation
```

### After (Controlled)

```
EKS Cluster During Node Upgrade

Production Apps (PDB-protected):
  ├─ Payment Service (min 2 available)
  ├─ Auth Service (min 2 available)
  ├─ Search Service (min 1 available)
  └─ etc (all have PDB)

Node drains:
  PDB: "You can evict 1 at a time"
  Kubernetes respects this

  Sequence:
    1. Evict pod-1 from node
    2. Pod-1 scheduled on another node (PDB allows, capacity exists)
    3. Evict pod-2 from node
    4. Pod-2 scheduled on another node
    5. ... continue until node is drained

  Duration: 5-10 minutes instead of thundering herd

Result:
  🟢 Services stay online
  🟢 Brief capacity reduction (traffic queued)
  🟢 Automatic recovery
  🟢 Zero manual intervention
  🟢 Customer doesn't notice
```

---

## The Discipline Bonus

### Namespace Labels as Organizational Intent

```yaml
# Production namespace
apiVersion: v1
kind: Namespace
metadata:
  name: production
  labels:
    pdb-min-available: "2"      # We care about this
    pdb-max-unavailable: "1"    # We plan capacity
    owner-team: platform        # Business context
    criticality: high
    budget-tier: tier-1
    ip-space-budget: "500"      # We expect up to 500 pods

---
# Dev namespace
apiVersion: v1
kind: Namespace
metadata:
  name: dev
  labels:
    owner-team: engineering
    criticality: low
    # No PDB labels = temporary deployments
```

### What Labels Tell Us

**Ops Dashboard:**
```
Namespaces with PDB constraints:
  ├─ production (min:2, max:1)     ← Heavily monitored, SLAs
  ├─ staging (min:1, max:1)         ← Pre-prod, important
  ├─ platform-system (min:2, max:1) ← Core infrastructure

Namespaces without constraints:
  ├─ dev       ← Temporary, no SLA
  ├─ testing   ← Experimental
  └─ scratch   ← Throwaway
```

**Team Communication:**
- "Deploy to production" → Implies production-grade reliability
- "We're in dev" → Implies temporary, single-replica, acceptable downtime
- No ambiguity, no surprises

---

## IP Space Visibility

### Capacity Planning Made Simple

```
Subnet: 10.0.0.0/22 = 1024 IPs
Reserved: 50 IPs (AWS)
Nodes: 50 × 2 IPs = 100 IPs
Available for Pods: 874 IPs

Namespace Budgets (from labels):
  production-api:      200 IPs (max 200 replicas)
  production-worker:   250 IPs (max 250 replicas)
  staging:             100 IPs (max 100 replicas)
  dev:                 100 IPs (temporary, flexible)
  ─────────────────────────
  Total budgeted:      650 IPs
  Remaining buffer:    224 IPs (25%)
  ✅ Healthy headroom
```

**When a team wants to scale:**
```bash
# Query: Can we scale Search Service to 200 replicas?
kubectl get ns production -o jsonpath='.metadata.labels.ip-space-budget'
# Returns: 500

# Current usage:
kubectl get deployment search -o jsonpath='.spec.replicas'
# Returns: 50

# Calculation:
#   Current: 50 pods
#   Requested: 200 pods
#   During rolling update: 50 + 200 = 250 IPs
#   Budget: 500 IPs
#   Available: 250 IPs
#   ✅ Approval: "You have room"
```

---

## Types of Deployments

### Pattern 1: True Production (Must Have PDB)

```yaml
namespace: production
labels:
  pdb-min-available: "2"
  pdb-max-unavailable: "1"

deployment:
  replicas: 3+
  SLA: 99.9%
  Upgrade window: On-demand (PDB allows anytime)
  Cost: High (many replicas)
```

**Enforcement:** Webhook rejects deployments without matching PDB.

---

### Pattern 2: Standard Workload (Should Have PDB)

```yaml
namespace: staging
labels:
  pdb-min-available: "1"
  pdb-max-unavailable: "1"

deployment:
  replicas: 2+
  SLA: 99%
  Upgrade window: Scheduled (coordinate with team)
  Cost: Medium
```

**Enforcement:** PDB recommended but not required (team discipline).

---

### Pattern 3: Development (No PDB)

```yaml
namespace: dev
labels: {}  # No PDB labels

deployment:
  replicas: 1
  SLA: None (expected downtime)
  Upgrade window: Anytime
  Cost: Minimal
```

**Enforcement:** No validation, pods may be evicted without notice.

---

## EKS Upgrade Timeline

### Without This Webhook (2+ hour outages)

```
Time    Event
────────────────────────────────────────────────
10:00   AWS: "Control plane upgrade starting"
10:30   AWS: "Control plane upgrade complete"
10:31   You: "Starting worker node replacement"
10:32   Node: "Evicting all pods..."
10:32   All single-replica services down 🔴
10:32   Alerting fires (thundering herd)
10:45   Manual intervention: Scale up services
11:00   Stability restored (partial)
11:30   Full recovery (with manual fixes)
12:00   Postmortem meeting
```

### With This Webhook (5-15 minute graceful rollout)

```
Time    Event
────────────────────────────────────────────────
10:00   AWS: "Control plane upgrade starting"
10:30   AWS: "Control plane upgrade complete"
10:31   You: "Starting worker node replacement"
10:32   Node: "Evicting pod-1 (respects PDB)"
10:32   Pod-1: Scheduled on another node
10:33   Node: "Evicting pod-2 (respects PDB)"
10:33   Pod-2: Scheduled on another node
...
10:45   All pods rebalanced, node ready for replacement
10:50   Next node replacement begins
...
11:15   All nodes replaced, cluster healthy
11:15   Services report: "No downtime detected"
11:15   Automatic recovery complete ✅
```

---

## Real-World Impact

### Story: Payment Team

**Before:**
```
08:00 EKS upgrade announced
08:15 Payment service goes down
08:20 PagerDuty alert
08:22 Team gets paged (sleep disrupted)
09:00 Manual recovery, service restored
09:30 Incident report written
10:00 Postmortem scheduled
→ Lost: 1 hour work, 1 incident, team stress
```

**After:**
```
08:00 EKS upgrade announced
08:15 Payment service remains online (PDB enforces 2 replicas)
08:20 No alert (capacity reduced temporarily, but service up)
08:45 EKS upgrade complete
08:45 Service auto-recovers to full capacity
→ Cost: 0 incidents, 0 manual work, team sleeps soundly
```

---

## Implementation Checklist

- ✅ Namespace labeling enforces discipline
- ✅ Automatic PDB creation removes friction
- ✅ Validation webhook prevents accidents
- ✅ Support for Deployments and StatefulSets
- ✅ StatefulSets get ordinal-order updates (maxUnavailable=1)
- ✅ Bare pods rejected in enforced namespaces
- ✅ IP space budgeting via labels
- ✅ Team communication made explicit

---

## Success Metrics

**You know this is working when:**

1. ✅ Zero unplanned downtime during EKS upgrades
2. ✅ Namespace labels match business criticality
3. ✅ Team discusses "Is this production?" before deploying
4. ✅ IP space planning conversations happen upfront
5. ✅ No single-replica production services
6. ✅ Postmortem meetings decrease
7. ✅ On-call fatigue reduced

---

## Next Steps

1. **Label your namespaces** - Map business criticality to PDB constraints
2. **Deploy this webhook** - Enable automatic PDB creation and validation
3. **Test during non-upgrade** - Verify PDB behavior with manual pod evictions
4. **Schedule cluster upgrade** - Run during normal business hours, not emergency window
5. **Monitor dashboards** - Track PDB disruptions and pod eviction counts
6. **Iterate constraints** - Adjust min/max based on actual usage patterns

See individual documentation files for technical details:
- `SEMANTIC.md` - Label-based enforcement rules
- `STATEFULSET_SUPPORT.md` - StatefulSet cardinality handling
- `BARE_POD_REJECTION.md` - Pod enforcement and conditional rejection
- `UNIT_TESTS.md` - Test coverage and validation
- `CLAUDE.md` - Original requirements and project scope
