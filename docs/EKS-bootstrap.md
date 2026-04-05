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

## Monitoring Checklist

| What to alert on | Why |
|---|---|
| `webhook-system` pod count < 3 | Enforcement gap risk |
| PDB `DisruptionsAllowed` = 0 for > 5 min | Upgrade may be stalled |
| Webhook response latency > 5s | Approaching `timeoutSeconds: 10` |
| Webhook pod restart count increasing | Instability — investigate before upgrade |
| ECR image pull failures | Will cause pods to not become Ready, risking forced drain |
