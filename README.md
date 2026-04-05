# Kubernetes Validating Admission Webhook for PDB Enforcement

A production-ready Validating Admission Webhook built in Go that enforces a governance rule: every `Deployment` in a Kubernetes cluster must have a matching `PodDisruptionBudget` (PDB) in the same namespace.

## Important — For Production Use, Consider Kyverno

> **This project is intentionally built from scratch as a learning reference.**
> It teaches you exactly how Kubernetes admission webhooks work under the hood —
> TLS handshake, `AdmissionReview` parsing, PDB label matching, mutating vs validating
> webhooks, HA topology, and EKS upgrade safety.
>
> **If you are building for production**, you are strongly encouraged to use
> **[Kyverno](https://kyverno.io)** instead. Kyverno is a CNCF policy engine that
> accomplishes everything this project does — PDB enforcement, auto-PDB generation,
> audit mode, policy reports — with zero Go code, a maintained upstream, and a
> rich policy library.
>
> | Goal | Use |
> |---|---|
> | Understand how admission webhooks work | **This project** |
> | Enforce PDBs in production with minimal overhead | **[Kyverno](https://kyverno.io)** |
>
> See [`docs/KYVERNO-PRIMER.md`](docs/KYVERNO-PRIMER.md) for a full comparison and
> ready-to-use Kyverno policies for PDB enforcement.

---

## Overview

This webhook system uses **two complementary webhooks** to ensure all Deployments have matching PodDisruptionBudgets:

1. **Mutating Webhook** — Auto-creates default PDBs when missing (user-friendly)
2. **Validating Webhook** — Ensures PDB compliance (strict enforcement & safety net)

### How It Works

```
Deployment CREATE request
    ↓
Mutating Webhook (pdb-webhook-mutate)
    ├─ Check if PDB exists in namespace
    ├─ If not found → Auto-create PDB (minAvailable: 2, maxUnavailable: 4)
    └─ Allow deployment to proceed
    ↓
Validating Webhook (pdb-webhook)
    ├─ Verify PDB exists with matching selector
    ├─ If match found → ALLOW
    └─ If no match → REJECT (safety net)
```

**Design Goals:**
- **User-friendly**: Auto-create PDBs (no manual work)
- **Configurable**: Set PDB defaults per namespace via ConfigMap
- **Strict enforcement**: Validating webhook catches edge cases
- **Minimal latency**: ~5-50ms per request
- **High availability (HA)**: 2 replicas, pod anti-affinity
- **Production-grade security**: TLS, RBAC, nonroot containers
- **Clear error messages**: Help operators understand rejections

## Project Structure

```
admission-controller/
├── CLAUDE.md                                  # Project requirements and standards
├── DEPLOYMENT.md                             # Step-by-step deployment guide
├── README.md                                 # This file
├── IMPLEMENTATION_CHECKLIST.md               # Verification of all deliverables
├── go.mod                                    # Go module definition
├── Dockerfile                                # Multi-stage build (distroless final)
├── cmd/
│   └── webhook/
│       └── main.go                           # Webhook server entry point
├── internal/
│   └── handler/
│       ├── validate.go                       # Validating webhook logic
│       └── mutate.go                         # Mutating webhook logic (auto-create PDB)
├── manifests/
│   ├── namespace.yaml                        # webhook-system namespace
│   ├── serviceaccount.yaml                   # Service account for webhook
│   ├── clusterrole.yaml                      # Permission to read PDBs
│   ├── clusterrolebinding.yaml               # Bind role to service account
│   ├── certificate.yaml                      # TLS cert (cert-manager + self-signed issuer)
│   ├── deployment.yaml                       # Webhook server deployment (2 replicas)
│   ├── service.yaml                          # ClusterIP service, port 443 → 8443
│   ├── mutatingwebhookconfiguration.yaml     # Auto-create PDB webhook
│   ├── validatingwebhookconfiguration.yaml   # Enforce PDB compliance webhook
│   └── pdb-config-example.yaml               # Example: per-namespace PDB config
└── test/
    ├── deployment-with-pdb.yaml              # Test case: explicit PDB (should pass)
    ├── deployment-without-pdb.yaml           # Test case: no PDB (gets auto-created)
    └── deployment-auto-pdb.yaml              # Test case: verify auto-creation works
```

## Quick Start

### 1. Build the image

```bash
docker build -t pdb-webhook:latest .
```

### 2. Deploy to cluster

See [DEPLOYMENT.md](DEPLOYMENT.md) for step-by-step instructions including cert-manager setup.

TL;DR (assuming cert-manager is installed):

```bash
kubectl apply -f manifests/namespace.yaml
kubectl apply -f manifests/serviceaccount.yaml
kubectl apply -f manifests/clusterrole.yaml
kubectl apply -f manifests/clusterrolebinding.yaml
kubectl apply -f manifests/certificate.yaml
kubectl apply -f manifests/deployment.yaml
kubectl apply -f manifests/service.yaml
kubectl apply -f manifests/mutatingwebhookconfiguration.yaml
kubectl apply -f manifests/validatingwebhookconfiguration.yaml  # LAST
```

### 3. Enable auto-PDB creation for a namespace

```bash
# Add labels to enable auto-creation with minAvailable: 2, maxUnavailable: 4
kubectl label namespace default \
  pdb-webhook.awanipro.com/min-available=2 \
  pdb-webhook.awanipro.com/max-unavailable=4
```

### 4. Test it

```bash
# Auto-created PDB (namespace has labels)
kubectl apply -f test/deployment-auto-pdb.yaml  # Should succeed, PDB auto-created

# Explicit PDB (always allowed if it matches)
kubectl apply -f test/deployment-with-pdb.yaml  # Should succeed

# Rejected (no namespace labels, no explicit PDB)
kubectl apply -f test/deployment-without-pdb.yaml  # Should fail if default namespace has no labels
```

## How It Works

1. **Intercept**: Deployment CREATE/UPDATE requests are intercepted by the API server before admission.
2. **Validate**: Webhook checks if a PDB exists in the same namespace with a selector matching the deployment's pod template labels.
3. **Respond**: Return `allowed: true` if match found, `allowed: false` with a clear message otherwise.

### Webhook Endpoints

**Mutating Webhook**
- **URL**: `POST https://pdb-webhook.webhook-system.svc/mutate`
- **Operation**: CREATE on Deployments
- **Behavior**: Auto-creates default PDB if missing
- **Failure Policy**: Ignore (deployment proceeds even if webhook fails)
- **Config Source**: Namespace ConfigMap `pdb-config` (if present)

**Validating Webhook**
- **URL**: `POST https://pdb-webhook.webhook-system.svc/validate`
- **Operations**: CREATE, UPDATE on Deployments
- **Behavior**: Ensures PDB exists with matching selector
- **Failure Policy**: Fail (deployment rejected if webhook unavailable)

**Common**
- **TLS**: Required (cert-manager + self-signed CA)
- **Payload**: `AdmissionReview` (JSON)
- **Response**: `AdmissionReview` (JSON)

### Matching Logic

The webhook matches a PDB to a deployment if:

1. The PDB exists in the deployment's namespace
2. The PDB has a non-nil `selector` (nil selectors match no pods)
3. The PDB's label selector matches **all** labels in the deployment's pod template

Example:

```yaml
# Deployment with these pod template labels:
app: nginx
tier: web
version: 1.26

# Will be ALLOWED by a PDB with selector:
matchLabels:
  app: nginx
  tier: web
  version: 1.26

# Or by a PDB with selector:
matchExpressions:
  - key: app
    operator: In
    values: ["nginx"]

# But will be REJECTED by a PDB with selector:
matchLabels:
  app: nginx  # Missing tier and version
```

## Configuration

### Server Environment Variables

| Variable | Default | Purpose |
|----------|---------|---------|
| `TLS_CERT_FILE` | `/tls/tls.crt` | Path to TLS certificate |
| `TLS_KEY_FILE` | `/tls/tls.key` | Path to TLS private key |
| `LISTEN_PORT` | `:8443` | Port to listen on |

### PDB Auto-creation (Opt-in Per-Namespace)

The mutating webhook **only** auto-creates PDBs if the namespace has these labels:
- `pdb-webhook.awanipro.com/min-available: <number>`
- `pdb-webhook.awanipro.com/max-unavailable: <number>`

**If the labels are not present**, the webhook will NOT auto-create PDBs, and the validating webhook will enforce that deployments have explicit PDBs.

**To enable auto-PDB creation**, add labels to the namespace:

```bash
# Enable auto-creation with minAvailable: 2, maxUnavailable: 4
kubectl label namespace default \
  pdb-webhook.awanipro.com/min-available=2 \
  pdb-webhook.awanipro.com/max-unavailable=4 \
  --overwrite
```

**Example namespace configurations:**

```yaml
# Strict (production)
apiVersion: v1
kind: Namespace
metadata:
  name: production
  labels:
    pdb-webhook.awanipro.com/min-available: "3"
    pdb-webhook.awanipro.com/max-unavailable: "1"
---
# Lenient (staging)
apiVersion: v1
kind: Namespace
metadata:
  name: staging
  labels:
    pdb-webhook.awanipro.com/min-available: "1"
    pdb-webhook.awanipro.com/max-unavailable: "2"
---
# No auto-creation (manual PDBs required)
apiVersion: v1
kind: Namespace
metadata:
  name: no-auto-pdb
  # No labels = no auto-creation
```

### Webhook Configuration

Edit manifest files to adjust:

- `manifests/mutatingwebhookconfiguration.yaml`:
  - `failurePolicy`: `Ignore` (deployment proceeds if webhook fails)
  - `timeoutSeconds`: How long to wait (default: 10s)

- `manifests/validatingwebhookconfiguration.yaml`:
  - `failurePolicy`: `Fail` (deployment rejected if webhook fails)
  - `timeoutSeconds`: How long to wait (default: 10s)
  - `namespaceSelector`: Which namespaces are subject to the webhook

## Operational Details

### TLS & Certificate Management

- Uses `cert-manager` for automatic certificate generation and rotation
- Self-signed issuer (no external CA needed)
- Certificate has 90-day validity with auto-rotation

### High Availability

- 2 replicas by default with pod anti-affinity
- Probes: Liveness (10s interval) + Readiness (5s interval)
- Graceful shutdown: 30s timeout on SIGTERM/SIGINT

### Security

- Runs as non-root user (UID 65532)
- Read-only root filesystem
- No capabilities
- NetworkPolicy-ready (minimal network exposure)

### Failure Policy

- `failurePolicy: Fail`: If webhook is unreachable, the API server **rejects** the deployment
- Ensures PDB compliance even if webhook has issues, but requires high availability

## Limitations & Trade-offs

1. **Label Matching**: Uses exact label matching on pod template labels, not deployment labels. This is correct per Kubernetes semantics (pods run with template labels).

2. **Namespaced Scope**: Each deployment must have a PDB in the same namespace. Cross-namespace PDBs are not supported.

3. **Latency**: Each admission request incurs a 10s timeout window. For typical small clusters, latency is <100ms.

4. **Strict Enforcement**: Using `failurePolicy: Fail` means any webhook outage prevents deployments. For production, ensure HA and monitoring.

## Monitoring

Key metrics to monitor:

```bash
# Webhook availability
kubectl get validatingwebhookconfigurations pdb-webhook

# Pod health
kubectl get pods -n webhook-system -l app.kubernetes.io/name=pdb-webhook

# Logs
kubectl logs -n webhook-system -l app.kubernetes.io/name=pdb-webhook -f

# Resource usage
kubectl top pod -n webhook-system -l app.kubernetes.io/name=pdb-webhook
```

## Development

### Building Locally

```bash
go mod download
CGO_ENABLED=0 GOOS=linux go build -o webhook ./cmd/webhook
```

### Running Tests

```bash
kubectl apply -f test/deployment-with-pdb.yaml  # Should succeed
kubectl apply -f test/deployment-without-pdb.yaml  # Should fail
```

### Code Structure

- **`cmd/webhook/main.go`**: Server setup, TLS, signal handling
- **`internal/handler/validate.go`**: Admission logic, PDB matching
- **`manifests/`**: Kubernetes resources (YAML)
- **`Dockerfile`**: Multi-stage build (builder + distroless runtime)

## Troubleshooting

See [DEPLOYMENT.md](DEPLOYMENT.md) for detailed troubleshooting guides covering:

- Webhook not registered
- Webhook not responding
- TLS certificate issues
- Deployment rejection when it shouldn't be

## License

This project is provided as-is for educational and operational use.

## References

- [Kubernetes Admission Webhooks](https://kubernetes.io/docs/reference/access-authn-authz/extensible-admission-controllers/)
- [PodDisruptionBudget API](https://kubernetes.io/docs/reference/kubernetes-api/workload-resources/pod-disruption-budget-v1/)
- [cert-manager Documentation](https://cert-manager.io/docs/)
- [Distroless Docker Images](https://github.com/GoogleContainerTools/distroless)

## Production Alternative — Kyverno

- [Kyverno](https://kyverno.io) — Kubernetes-native policy engine (recommended for production)
- [Kyverno — Require PDB Policy](https://kyverno.io/policies/other/require-pdb/require-pdb/) — ready-made, zero code
- [Kyverno — Auto-create PDB Policy](https://kyverno.io/policies/other/create-default-pdb/create-default-pdb/)
- [Kyverno — High Availability Guide](https://kyverno.io/docs/guides/high-availability/)
- [docs/KYVERNO-PRIMER.md](docs/KYVERNO-PRIMER.md) — full primer and comparison in this repo
