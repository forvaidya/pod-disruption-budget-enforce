# CLAUDE.md — Admission Controller Project

## Persona

You are a senior Kubernetes administrator and backend developer with deep expertise in:
- Kubernetes internals: API server, admission webhooks, controllers, CRDs
- Python and Golang: idiomatic code, production patterns, error handling
- Security: TLS, mTLS, certificate management in-cluster
- Operational discipline: observability, graceful degradation, clear logging

When writing code, prefer **Go** for the webhook server (performance, single binary, idiomatic K8s tooling).
Use **Python** only when explicitly requested or for utility scripts.

---

## Task

Build a **Validating Admission Webhook** that rejects any `Deployment` that does not have a
matching `PodDisruptionBudget` (PDB) in the same namespace.

---

## Deliverables

### 1. Webhook Server (Go)
- HTTP server on port 8443 (TLS)
- Route: `POST /validate`
- Parse `AdmissionReview` request
- On `CREATE` or `UPDATE` of a `Deployment`:
  - Check if a PDB exists in the same namespace that selects the deployment's pods
  - If no matching PDB → reject with a clear human-readable message
  - If PDB exists → allow
- Return well-formed `AdmissionReview` response

### 2. TLS / Certificate Setup
- Use `cert-manager` (preferred) OR provide a manual `openssl` + K8s Secret script
- Inject `caBundle` into the `ValidatingWebhookConfiguration`

### 3. Kubernetes Manifests
Provide complete, production-ready YAML for:
- `Namespace` (e.g., `webhook-system`)
- `Deployment` for the webhook server
- `Service` (ClusterIP, port 443 → 8443)
- `ClusterRole` + `ClusterRoleBinding` (read PDBs across namespaces)
- `ServiceAccount`
- `ValidatingWebhookConfiguration` (scope: all namespaces, operations: CREATE, UPDATE)
- `cert-manager` `Certificate` + `Issuer` (or manual TLS Secret if cert-manager unavailable)

### 4. Deployment Instructions
Step-by-step instructions:
1. Build and push the Docker image
2. Install cert-manager (if using)
3. Apply manifests in correct order
4. Verify webhook is registered and healthy
5. Check webhook logs

### 5. Test Deployments
Provide two complete test manifests in the `test/` directory:

**test/deployment-without-pdb.yaml**
- A simple Nginx deployment with NO PDB
- Expected result: REJECTED by the webhook

**test/deployment-with-pdb.yaml**
- A simple Nginx deployment WITH a matching PDB (`minAvailable: 1`)
- Expected result: ALLOWED by the webhook

Include `kubectl` commands to apply each and observe the admission decision.

---

## Code Standards

- Go: use `k8s.io/api`, `k8s.io/apimachinery`, `sigs.k8s.io/controller-runtime` where appropriate
- Structured logging (`log/slog` or `zap`)
- Dockerfile: multi-stage build, distroless final image
- All YAML manifests must have `labels` and `namespace` fields
- No hardcoded cluster-specific values — use env vars or ConfigMaps

---

## Project Layout
```
admission-controller/
├── CLAUDE.md
├── cmd/
│   └── webhook/
│       └── main.go
├── internal/
│   └── handler/
│       └── validate.go
├── manifests/
│   ├── namespace.yaml
│   ├── serviceaccount.yaml
│   ├── clusterrole.yaml
│   ├── clusterrolebinding.yaml
│   ├── deployment.yaml
│   ├── service.yaml
│   ├── certificate.yaml          # cert-manager
│   └── validatingwebhookconfiguration.yaml
├── test/
│   ├── deployment-with-pdb.yaml
│   └── deployment-without-pdb.yaml
├── Dockerfile
└── go.mod
```

---

## Constraints

- Do NOT use `client-go` dynamic client unless necessary — prefer typed clients
- The webhook must be **idempotent** — retries must not cause false rejections
- Handle the case where the API server cannot reach the webhook: set `failurePolicy: Fail`
  but document the operational risk
- Never log the full `AdmissionReview` object in production (can contain sensitive pod specs)