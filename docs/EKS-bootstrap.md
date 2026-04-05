# EKS Bootstrap Guide — PDB Webhook High Availability

## Overview

This guide covers how to provision EKS with the correct node group topology to ensure
the PDB enforcement webhook **never goes down**, including during node upgrades.

The webhook uses `failurePolicy: Fail` — if it becomes unavailable, the API server
rejects all Deployment/StatefulSet operations cluster-wide. The infrastructure below
is non-negotiable for production.

---

## Node Group Architecture

### Two Separate Managed Node Groups

```
┌─────────────────────────────────────────────────────────┐
│                        EKS Cluster                      │
│                                                         │
│  ┌──────────────────────────┐  ┌──────────────────────┐ │
│  │  Node Group: webhook-    │  │  Node Group: app-    │ │
│  │  infra                   │  │  workloads           │ │
│  │                          │  │                      │ │
│  │  AZ-a    AZ-b    AZ-c   │  │  AZ-a  AZ-b  AZ-c   │ │
│  │  node1   node2   node3  │  │  ...   ...   ...     │ │
│  │    │       │       │    │  │                      │ │
│  │  wh-pod  wh-pod  wh-pod │  │  app pods            │ │
│  │  wh-pod                 │  │                      │ │
│  └──────────────────────────┘  └──────────────────────┘ │
│                                                         │
│  Upgrade sequence: webhook-infra FIRST → app-workloads  │
└─────────────────────────────────────────────────────────┘
```

**Why separate node groups?**
- You can upgrade the webhook node group independently and first
- Application workloads never interfere with webhook scheduling
- Node group labels cleanly pin webhook pods to the right nodes

---

## Node Group Configuration

### 1. webhook-infra Node Group

| Setting | Value | Reason |
|---|---|---|
| Min nodes | 3 | One per AZ minimum |
| Desired nodes | 3 | Matches replica count baseline |
| Max nodes | 6 | Headroom for rolling updates |
| AZs | 3 (one per AZ) | Hard requirement for AZ spread |
| Instance type | `m5.large` or similar | Webhook is lightweight |
| Label | `node.kubernetes.io/role: webhook-infra` | Pod affinity pin |
| Taint (optional) | `dedicated=webhook-infra:NoSchedule` | Prevent non-webhook pods |

**eksctl example:**
```yaml
nodeGroups:
  - name: webhook-infra
    instanceType: m5.large
    minSize: 3
    desiredCapacity: 3
    maxSize: 6
    availabilityZones:
      - us-east-1a
      - us-east-1b
      - us-east-1c
    labels:
      node.kubernetes.io/role: webhook-infra
    taints:
      - key: dedicated
        value: webhook-infra
        effect: NoSchedule
```

**Terraform (aws_eks_node_group) snippet:**
```hcl
resource "aws_eks_node_group" "webhook_infra" {
  cluster_name    = aws_eks_cluster.main.name
  node_group_name = "webhook-infra"
  node_role_arn   = aws_iam_role.node.arn
  subnet_ids      = [subnet_az_a, subnet_az_b, subnet_az_c]  # one per AZ

  scaling_config {
    min_size     = 3
    desired_size = 3
    max_size     = 6
  }

  labels = {
    "node.kubernetes.io/role" = "webhook-infra"
  }

  taint {
    key    = "dedicated"
    value  = "webhook-infra"
    effect = "NO_SCHEDULE"
  }
}
```

### 2. app-workloads Node Group

Standard managed node group. No special constraints beyond spanning multiple AZs.
Upgrade this group **only after** confirming webhook pods are fully healthy.

---

## Webhook Pod Scheduling Guarantees

The `manifests/deployment.yaml` enforces the following (do not remove these):

### Node Affinity — hard pin to webhook-infra nodes
```yaml
nodeAffinity:
  requiredDuringSchedulingIgnoredDuringExecution:
    nodeSelectorTerms:
      - matchExpressions:
          - key: node.kubernetes.io/role
            operator: In
            values: ["webhook-infra"]
```
Webhook pods will never land on application nodes.

### AZ Spread — hard, no stacking
```yaml
topologySpreadConstraints:
  - maxSkew: 1
    topologyKey: topology.kubernetes.io/zone
    whenUnsatisfiable: DoNotSchedule
```
Pods are spread across AZs. Scheduler rejects placement that would create AZ imbalance.

### Node Spread — hard, one pod per node
```yaml
podAntiAffinity:
  requiredDuringSchedulingIgnoredDuringExecution:
    - topologyKey: kubernetes.io/hostname
```
No two webhook pods ever share a node. A single node failure never takes out more than one pod.

### If you add the taint to the node group, add this toleration to deployment.yaml:
```yaml
tolerations:
  - key: dedicated
    value: webhook-infra
    effect: NoSchedule
```

---

## PodDisruptionBudget

`manifests/pdb-webhook.yaml` sets `minAvailable: 3`.

During any voluntary disruption (node drain, EKS upgrade, manual cordon):
- EKS will **block the drain** until a replacement webhook pod is Running and Ready
- The enforcement mechanism stays live throughout the upgrade
- EKS managed node group upgrade timeout is **15 minutes** — if the new pod does not
  become Ready in that window, EKS will force-drain regardless. Monitor image pull times
  and ensure the webhook image is available in ECR in the same region.

---

## EKS Node Upgrade Sequence

**Always follow this order. Never upgrade app-workloads first.**

### Step 1 — Verify webhook health before starting
```bash
kubectl get pods -n webhook-system
# All 4 pods must be Running and Ready

kubectl get pdb -n webhook-system
# ALLOWED DISRUPTIONS must be >= 1
```

### Step 2 — Upgrade webhook-infra node group
```bash
# Via AWS Console: EKS → Cluster → Node Groups → webhook-infra → Update now
# Or via eksctl:
eksctl upgrade nodegroup \
  --cluster=<cluster-name> \
  --name=webhook-infra \
  --kubernetes-version=<target-version>
```

EKS will:
1. Launch a new node with the updated AMI
2. Cordon the old node
3. Check PDB — drain blocked until replacement pod is Ready
4. Drain old node only when `minAvailable: 3` is satisfied
5. Repeat for each node in the group

### Step 3 — Verify webhook health after upgrade
```bash
kubectl get pods -n webhook-system -o wide
# All pods Running, spread across different nodes and AZs

kubectl describe pdb pdb-webhook -n webhook-system
# Disruptions Allowed: 1

kubectl get endpoints pdb-webhook -n webhook-system
# All 4 pod IPs must be listed
```

### Step 4 — Only then upgrade app-workloads node group
```bash
eksctl upgrade nodegroup \
  --cluster=<cluster-name> \
  --name=app-workloads \
  --kubernetes-version=<target-version>
```

---

## Break-Glass: Deadlock Recovery

If the cluster deadlocks (webhook down, API server rejecting all operations):

```bash
# 1. Temporarily switch to Ignore to unblock the API server
kubectl patch validatingwebhookconfiguration pdb-webhook \
  --type='json' \
  -p='[{"op":"replace","path":"/webhooks/0/failurePolicy","value":"Ignore"}]'

# 2. Force-delete stuck webhook pods if needed
kubectl delete pods -n webhook-system --all --force --grace-period=0

# 3. Wait for pods to reschedule and become Ready
kubectl wait --for=condition=Ready pods -n webhook-system -l app.kubernetes.io/name=pdb-webhook --timeout=120s

# 4. Restore failurePolicy to Fail
kubectl patch validatingwebhookconfiguration pdb-webhook \
  --type='json' \
  -p='[{"op":"replace","path":"/webhooks/0/failurePolicy","value":"Fail"}]'
```

> Always alert on this event. A deadlock means a gap in PDB enforcement occurred.
> Audit what Deployments were created during the Ignore window.

---

## Bootstrap Acceptance Checklist

**The cluster must NOT be handed to workload teams until every item below is checked and passing.
Run this checklist once at initial bootstrap and once after every Kubernetes version upgrade.**

### Infrastructure Gates

```bash
# 1. webhook-infra node group exists with nodes in 3 AZs
kubectl get nodes -l node.kubernetes.io/role=webhook-infra \
  -o custom-columns='NAME:.metadata.name,AZ:.metadata.labels.topology\.kubernetes\.io/zone'
# Expected: 3+ nodes, each in a different AZ

# 2. All 4 webhook pods Running and Ready
kubectl get pods -n webhook-system -o wide
# Expected: 4/4 Running, spread across different NODES and AZs

# 3. PDB is active and allows at least 1 disruption
kubectl get pdb pdb-webhook -n webhook-system
# Expected: MIN AVAILABLE=3, ALLOWED DISRUPTIONS=1

# 4. Endpoints registered (all 4 pod IPs present)
kubectl get endpoints pdb-webhook -n webhook-system
# Expected: 4 IPs listed
```

### Drain Test — Mandatory

**Do not skip this. Assuming PDB works without testing is not acceptable.**

```bash
# Pick any webhook-infra node
NODE=$(kubectl get nodes -l node.kubernetes.io/role=webhook-infra -o jsonpath='{.items[0].metadata.name}')

# Attempt drain — this MUST block and show PDB eviction warning
kubectl drain $NODE --ignore-daemonsets --delete-emptydir-data
# Expected output includes:
# "Cannot evict pod as it would violate the pod's disruption budget"
# Drain hangs — this is correct behaviour

# Confirm 3 webhook pods still Running during the blocked drain
kubectl get pods -n webhook-system
# Expected: 3+ Running (the pod on the cordoned node may show Terminating
# only after PDB is satisfied by a replacement)

# Uncordon to restore
kubectl uncordon $NODE
```

### Webhook Enforcement Test

```bash
# Confirm webhook rejects a Deployment with no PDB
kubectl apply -f test/deployment-without-pdb.yaml
# Expected: admission webhook denied the request

# Confirm webhook allows a Deployment with a PDB
kubectl apply -f test/deployment-with-pdb.yaml
# Expected: deployment.apps/... created
```

### Sign-off

| Gate | Pass/Fail | Verified by | Date |
|---|---|---|---|
| 3 webhook-infra nodes across 3 AZs | | | |
| 4 webhook pods Running, spread across nodes | | | |
| PDB DisruptionsAllowed = 1 | | | |
| Drain test blocked by PDB | | | |
| Webhook rejects deployment-without-pdb | | | |
| Webhook allows deployment-with-pdb | | | |

> Cluster is not production-ready until all rows are Pass.

---

## Alternative: Kyverno as a Ready-Made Policy Engine

[Kyverno](https://kyverno.io) is a Kubernetes-native policy engine that can replace
this custom webhook for PDB enforcement — with zero Go code required.

### What Kyverno offers for this use case

| Capability | Kyverno | This custom webhook |
|---|---|---|
| PDB enforcement (validate) | Built-in policy from policy library | Custom Go handler |
| Auto-create PDB (mutate) | Built-in generate policy | Custom mutating handler |
| Policy-as-YAML, no code | Yes | No — requires Go + build pipeline |
| EKS Best Practices tag | Yes — officially tagged | N/A |
| Audit vs Enforce mode toggle | Yes | Requires code change |
| Policy reporting & audit logs | Built-in | Must build separately |
| Maintenance overhead | Low — upstream maintained | High — you own it |

### Official ready-made policy

Kyverno ships a `require-pdb` ClusterPolicy in its policy library, officially tagged
as an **EKS Best Practice**. It checks all incoming Deployments and StatefulSets
to ensure a matching PDB exists in the same namespace:

```yaml
apiVersion: kyverno.io/v1
kind: ClusterPolicy
metadata:
  name: require-pdb
  annotations:
    policies.kyverno.io/title: Require PodDisruptionBudget
    policies.kyverno.io/category: Sample, EKS Best Practices
    policies.kyverno.io/minversion: 1.6.0
    policies.kyverno.io/subject: Deployment, PodDisruptionBudget
    policies.kyverno.io/description: >-
      PodDisruptionBudget resources are useful to ensuring minimum availability
      is maintained at all times. This policy checks all incoming Deployments
      and StatefulSets to ensure they have a matching, preexisting PodDisruptionBudget.
spec:
  validationFailureAction: Enforce   # change to Audit for dry-run mode
  background: false
  rules:
    - name: require-pdb
      match:
        any:
          - resources:
              kinds:
                - Deployment
                - StatefulSet
      preconditions:
        all:
          - key: "{{request.operation || 'BACKGROUND'}}"
            operator: Equals
            value: CREATE
          - key: "{{ request.object.spec.replicas }}"
            operator: GreaterThanOrEquals
            value: 3
      context:
        - name: pdb_count
          apiCall:
            urlPath: /apis/policy/v1/namespaces/{{request.namespace}}/poddisruptionbudgets
            jmesPath: "items[?label_match(spec.selector.matchLabels, \
              `{{request.object.spec.template.metadata.labels}}`)] | length(@)"
      validate:
        message: "No matching PodDisruptionBudget found for this Deployment."
        deny:
          conditions:
            any:
              - key: "{{pdb_count}}"
                operator: LessThan
                value: 1
```

> Source: [kyverno.io/policies/other/require-pdb](https://kyverno.io/policies/other/require-pdb/require-pdb/)

### Kyverno HA on EKS — requirements

Kyverno itself is an admission webhook and faces the **same HA problem** as this
custom webhook. The same principles apply:

- **Minimum 3 replicas** for the admission controller — this is Kyverno's own documented minimum
- **Dedicated node group** — pin Kyverno pods to an infrastructure node group (same
  `webhook-infra` pattern described above)
- **PDB on Kyverno itself** — `minAvailable: 2` at minimum; Kyverno's Helm chart
  can configure this automatically
- **Upgrade Kyverno node group first** — same sequencing rule applies

```bash
# Install Kyverno via Helm in HA mode (3 replicas, PDB included)
helm repo add kyverno https://kyverno.github.io/kyverno/
helm repo update

helm install kyverno kyverno/kyverno \
  --namespace kyverno \
  --create-namespace \
  --set admissionController.replicas=3 \
  --set backgroundController.replicas=2 \
  --set cleanupController.replicas=2 \
  --set reportsController.replicas=2 \
  --set admissionController.podDisruptionBudget.enabled=true \
  --set admissionController.podDisruptionBudget.minAvailable=2
```

> Full HA guide: [kyverno.io/docs/guides/high-availability](https://kyverno.io/docs/guides/high-availability/)

### When to use Kyverno vs this custom webhook

**Choose Kyverno if:**
- You want policy-as-code with no build/deploy pipeline for the enforcer itself
- You need audit reporting, policy exceptions, or dry-run toggles out of the box
- You plan to enforce multiple policies beyond PDB (resource limits, image registries, labels, etc.)
- Team has limited Go expertise

**Keep this custom webhook if:**
- You need fine-grained label-based namespace enforcement logic (already built here)
- You need the mutating auto-PDB creation behaviour with custom defaults
- You want a minimal, single-purpose binary with no external policy engine dependency
- You already operate this in production and the operational cost is acceptable

### Additional Kyverno policies worth enabling alongside

| Policy | URL |
|---|---|
| `require-pdb` | https://kyverno.io/policies/other/require-pdb/require-pdb/ |
| `create-default-pdb` (auto-generate) | https://kyverno.io/policies/other/create-default-pdb/create-default-pdb/ |
| `require-reasonable-pdbs` | https://kyverno.io/policies/other/require-reasonable-pdbs/require-reasonable-pdbs/ |
| `deployment-replicas-higher-than-pdb` | https://kyverno.io/policies/other/deployment-replicas-higher-than-pdb/deployment-replicas-higher-than-pdb/ |
| `pdb-minavailable` | https://kyverno.io/policies/other/pdb-minavailable/pdb-minavailable/ |

---

## Monitoring Checklist

| What to alert on | Why |
|---|---|
| `webhook-system` pod count < 3 | Enforcement gap risk |
| PDB `DisruptionsAllowed` = 0 for > 5 min | Upgrade may be stalled |
| Webhook response latency > 5s | Approaching `timeoutSeconds: 10` |
| Webhook pod restart count increasing | Instability — investigate before upgrade |
| ECR image pull failures | Will cause pods to not become Ready, risking forced drain |
