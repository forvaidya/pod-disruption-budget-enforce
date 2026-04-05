# Kyverno — Elementary Knowledge Reference

> Captured during the design of the PDB enforcement webhook.
> This document preserves foundational understanding of Kyverno as a policy engine
> and how it compares to building a custom admission webhook.

---

## What is Kyverno?

Kyverno is a **Kubernetes-native policy engine** and a CNCF project (Incubating).
It runs as a dynamic admission controller inside your cluster — the same mechanism
this custom webhook uses — but provides a full policy framework so you write
**YAML policies instead of Go code**.

Official site: https://kyverno.io
GitHub: https://github.com/kyverno/kyverno

---

## How It Works — Architecture

```
kubectl apply Deployment
        ↓
Kubernetes API Server
        ↓
AdmissionReview request sent to Kyverno webhook
        ↓
Kyverno Engine evaluates the resource against all matching ClusterPolicies
        ↓
  Validate?  →  Allow / Deny with message
  Mutate?    →  Patch the resource before it is persisted
  Generate?  →  Create a related resource (e.g. auto-create a PDB)
        ↓
Response returned to API Server
```

### Internal Components

| Component | Role |
|---|---|
| **Webhook Server** | Receives `AdmissionReview` from API server, routes to Engine |
| **Webhook Controller** | Watches installed policies, dynamically registers/updates webhook configs |
| **Cert Renewer** | Manages TLS certificates for the webhook (no cert-manager required) |
| **Background Controller** | Handles `generate` and `mutate-existing` policies asynchronously |
| **Report Controllers** | Produces `PolicyReport` and `ClusterPolicyReport` CRDs with audit results |

---

## Three Things Kyverno Can Do

### 1. Validate — Enforce rules at admission time
Reject resources that don't comply. Equivalent to a `ValidatingWebhookConfiguration`.

```yaml
# Reject Deployments with no PDB
spec:
  validationFailureAction: Enforce  # or Audit
  rules:
    - name: require-pdb
      validate:
        message: "A PodDisruptionBudget is required."
        deny:
          conditions:
            - key: "{{pdb_count}}"
              operator: LessThan
              value: 1
```

### 2. Mutate — Modify resources before they are saved
Patch the resource at admission time. Equivalent to a `MutatingWebhookConfiguration`.

```yaml
# Auto-inject a label on all Deployments
spec:
  rules:
    - name: add-label
      mutate:
        patchStrategicMerge:
          metadata:
            labels:
              managed-by: kyverno
```

### 3. Generate — Create related resources automatically
When a Deployment is created, Kyverno can automatically create a PDB for it.

```yaml
# Auto-create a PDB when a Deployment is created
spec:
  rules:
    - name: create-default-pdb
      generate:
        kind: PodDisruptionBudget
        name: "{{request.object.metadata.name}}-pdb"
        namespace: "{{request.namespace}}"
        data:
          spec:
            minAvailable: 1
            selector:
              matchLabels: "{{request.object.spec.template.metadata.labels}}"
```

---

## Policy Modes

| Mode | Behaviour | Use case |
|---|---|---|
| `Audit` | Logs violations, allows the request | Dry-run, observability phase |
| `Enforce` | Blocks the request on violation | Production enforcement |

You can deploy in `Audit` first, observe `PolicyReport` CRDs, then flip to `Enforce`.
This is safer than a custom webhook which is typically enforce-only from day 1.

---

## Kyverno vs OPA Gatekeeper vs Custom Webhook

| | Kyverno | OPA Gatekeeper | Custom Webhook (this project) |
|---|---|---|---|
| Policy language | YAML + CEL | Rego (custom language) | Go code |
| Learning curve | Low — K8s native YAML | High — Rego is non-trivial | Medium — Go + K8s API |
| Validate | Yes | Yes | Yes |
| Mutate | Yes | Yes (newer) | Yes |
| Generate resources | Yes | No | No |
| Audit / dry-run mode | Yes — toggle per policy | Yes | No — requires code change |
| Policy reports | Built-in CRDs | Built-in | Must build |
| Policy library | Large — kyverno.io/policies | kube-policy-library | N/A |
| CNCF status | Incubating | Graduated | N/A |
| Multi-policy support | Excellent | Excellent | One webhook per concern |
| Resource efficiency | Better | Syncs full inventory to memory — can bottleneck | Minimal |
| Infra scope | K8s only | K8s + Terraform + Cloud | K8s only |
| Maintenance | Upstream maintained | Upstream maintained | You own it |

**When to choose Kyverno:**
- You want policy enforcement without maintaining a build pipeline
- You need audit mode, policy reports, or policy exceptions
- You plan to enforce many policies (labels, images, resource limits, PDBs, etc.)
- Your team is more ops/YAML-oriented than Go-oriented

**When OPA Gatekeeper wins:**
- You already use OPA/Rego for non-Kubernetes policy (Terraform, microservices authz)
- You want a single policy language across your entire stack

**When a custom webhook wins:**
- Your logic is complex and cannot be expressed in YAML/CEL
- You need tight control over behaviour, performance, and dependencies
- You want a minimal, single-purpose binary with no external engine

---

## Kyverno HA — What You Must Know

Kyverno is itself an admission webhook. If it goes down, and `failurePolicy: Fail`
is set, the API server blocks all matched resource operations — the same deadlock
risk as this custom webhook.

**Production requirements for Kyverno HA:**

| Setting | Value | Reason |
|---|---|---|
| `admissionController.replicas` | 3 | Min for HA; handles all admission requests |
| `backgroundController.replicas` | 2 | Leader election — only 1 active at a time |
| `cleanupController.replicas` | 2 | No leader election — both can handle deletions |
| `reportsController.replicas` | 2 | Leader election — only 1 active at a time |
| PDB on admission controller | `minAvailable: 2` | Blocks drain until replacement is Ready |
| Dedicated node group | `webhook-infra` | Same pattern as this project |
| Multi-AZ spread | Required | Survive AZ failure |

**Helm install (HA mode):**
```bash
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

> Full HA guide: https://kyverno.io/docs/guides/high-availability/

---

## Kyverno PDB Policy Library — Quick Reference

All ready-made, no code needed:

| Policy | What it does | URL |
|---|---|---|
| `require-pdb` | Reject Deployment/StatefulSet if no matching PDB exists | https://kyverno.io/policies/other/require-pdb/require-pdb/ |
| `create-default-pdb` | Auto-generate a PDB when a Deployment is created | https://kyverno.io/policies/other/create-default-pdb/create-default-pdb/ |
| `require-reasonable-pdbs` | Validates PDB allows ≥50% disruption (prevents overly strict PDBs) | https://kyverno.io/policies/other/require-reasonable-pdbs/require-reasonable-pdbs/ |
| `deployment-replicas-higher-than-pdb` | Ensures replicas > PDB minAvailable | https://kyverno.io/policies/other/deployment-replicas-higher-than-pdb/deployment-replicas-higher-than-pdb/ |
| `pdb-minavailable` | Validates PDB minAvailable is not equal to replica count | https://kyverno.io/policies/other/pdb-minavailable/pdb-minavailable/ |

---

## Key Takeaway

**Learn the pattern here. Use Kyverno in production.**

This project is intentionally built from scratch so you understand exactly what happens
inside a Kubernetes admission webhook:
- How `AdmissionReview` requests flow from the API server to a webhook
- How TLS, cert rotation, and `caBundle` injection work
- How PDB label selectors are matched against Deployment pod templates
- How mutating and validating webhooks compose together
- How HA topology, PDBs, and EKS node groups protect the enforcer itself

Once you understand the internals, **[Kyverno](https://kyverno.io)** gives you all of
this — and much more — without maintaining a Go codebase, a Docker build pipeline,
or custom webhook registration logic. It is actively maintained, CNCF-backed, and
has a growing policy library covering hundreds of Kubernetes best practices.

> Users are encouraged to **study this project** to understand the mechanics, then
> **adopt Kyverno** for real cluster policy enforcement.

---

## Sources

- [Kyverno — How Kyverno Works](https://kyverno.io/docs/introduction/how-kyverno-works/)
- [Kyverno — High Availability Guide](https://kyverno.io/docs/guides/high-availability/)
- [Kyverno — Require PDB Policy (EKS Best Practices)](https://kyverno.io/policies/other/require-pdb/require-pdb/)
- [Kyverno — Create Default PDB Policy](https://kyverno.io/policies/other/create-default-pdb/create-default-pdb/)
- [Nirmata — Kyverno vs OPA Gatekeeper (2025)](https://nirmata.com/2025/02/07/kubernetes-policy-comparison-kyverno-vs-opa-gatekeeper/)
- [Kyverno vs OPA — Zesty](https://zesty.co/finops-glossary/kyverno-vs-opa-kubernetes-policy-engines/)
- [EKS Workshop — Policy management with Kyverno](https://www.eksworkshop.com/docs/security/kyverno/)
- [GitHub — kyverno/kyverno](https://github.com/kyverno/kyverno)
