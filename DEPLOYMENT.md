# Deployment Instructions for PDB Admission Webhook

This guide walks through building, deploying, and testing the Validating Admission Webhook that enforces PodDisruptionBudget (PDB) requirements for all Deployments.

## Prerequisites

- Kubernetes cluster (1.24+)
- `kubectl` configured to access the cluster
- `cert-manager` installed (or use manual TLS setup)
- Docker for building the image
- Go 1.23 (if building locally)

## Step 1: Build and Push Docker Image

### Option A: Build and push to a registry

```bash
# Build the image
docker build -t <your-registry>/pdb-webhook:latest .

# Push to registry
docker push <your-registry>/pdb-webhook:latest

# Update the image reference in manifests/deployment.yaml
sed -i 's|localhost:5000/pdb-webhook:latest|<your-registry>/pdb-webhook:latest|g' manifests/deployment.yaml
```

### Option B: Load image into Kind cluster (for local testing)

```bash
docker build -t pdb-webhook:latest .
kind load docker-image pdb-webhook:latest
```

## Step 2: Install cert-manager (if not already installed)

```bash
# Install cert-manager
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.14.0/cert-manager.yaml

# Wait for cert-manager to be ready
kubectl wait --for=condition=Available --timeout=300s \
  deployment/cert-manager -n cert-manager \
  deployment/cert-manager-webhook -n cert-manager \
  deployment/cert-manager-cainjector -n cert-manager
```

## Step 3: Apply Manifests in Order

### Create namespace and RBAC

```bash
kubectl apply -f manifests/namespace.yaml
kubectl apply -f manifests/serviceaccount.yaml
kubectl apply -f manifests/clusterrole.yaml
kubectl apply -f manifests/clusterrolebinding.yaml
```

Verify RBAC is in place:

```bash
kubectl get sa -n webhook-system
kubectl get clusterrole pdb-webhook
kubectl get clusterrolebinding pdb-webhook
```

### Create TLS certificate

```bash
kubectl apply -f manifests/certificate.yaml

# Wait for certificate to be ready (this may take 10-30s)
kubectl wait --for=condition=Ready certificate/pdb-webhook-tls \
  -n webhook-system --timeout=60s

# Verify the secret was created with cert-manager injecting the certificate
kubectl get secret pdb-webhook-tls -n webhook-system -o yaml | grep -A 1 tls.crt
```

### Deploy webhook server

```bash
kubectl apply -f manifests/deployment.yaml
kubectl apply -f manifests/service.yaml

# Wait for the deployment to be ready
kubectl wait --for=condition=Available --timeout=60s \
  deployment/pdb-webhook -n webhook-system

# Verify pods are running
kubectl get pods -n webhook-system -l app.kubernetes.io/name=pdb-webhook
```

### Register the webhooks (THIS IS LAST)

**IMPORTANT**: Apply the webhook configurations LAST, after the webhook server is running and healthy.

```bash
# First apply the mutating webhook (auto-creates PDBs)
kubectl apply -f manifests/mutatingwebhookconfiguration.yaml

# Then apply the validating webhook (enforces PDB compliance)
kubectl apply -f manifests/validatingwebhookconfiguration.yaml

# Verify both webhooks are registered
kubectl get mutatingwebhookconfigurations pdb-webhook-mutate
kubectl get validatingwebhookconfigurations pdb-webhook
```

#### Webhook Order and Behavior

1. **Mutating Webhook** (`pdb-webhook-mutate`) - Runs FIRST on Deployment CREATE
   - Checks if namespace has PDB configuration labels:
     - `pdb-webhook.awanipro.com/min-available`
     - `pdb-webhook.awanipro.com/max-unavailable`
   - If labels exist → Auto-creates a PDB with those values
   - If labels missing → Skips auto-creation (no PDB is created)
   - `failurePolicy: Ignore` (deployment proceeds even if webhook fails)

2. **Validating Webhook** (`pdb-webhook`) - Runs AFTER mutation
   - Ensures every Deployment has a matching PDB
   - Rejects deployments without a PDB (if mutating webhook didn't create one)
   - Acts as a safety net for edge cases
   - `failurePolicy: Fail` (strict enforcement)

## Step 4: Verify Webhook is Healthy

### Check webhook registration

```bash
# Describe the webhook configuration
kubectl describe validatingwebhookconfigurations pdb-webhook

# Should show:
# - Name: pdb-webhook.webhook-system.svc
# - ClientConfig: pdb-webhook.webhook-system.svc:443/validate
# - caBundle: (non-empty, injected by cert-manager)
# - Failure Policy: Fail
# - Timeout: 10s
```

### Check webhook server logs

```bash
# View recent logs
kubectl logs -n webhook-system -l app.kubernetes.io/name=pdb-webhook --tail=50 -f

# Expected to see:
# "starting pdb-webhook"
# "starting TLS server"
```

### Check webhook connectivity

```bash
# Test webhook pod health check
kubectl exec -it -n webhook-system <pod-name> -- curl -k https://localhost:8443/healthz
```

## Step 5: Test the Webhooks

The webhook system has **two webhooks**:
1. **Mutating webhook** — Auto-creates PDBs for deployments
2. **Validating webhook** — Ensures every deployment has a PDB (safety net)

### Test 1: Auto-created PDB (Mutating Webhook)

```bash
# This deployment has NO explicit PDB, but the mutating webhook will auto-create one
kubectl apply -f test/deployment-auto-pdb.yaml

# Expected success:
# deployment.apps/nginx-auto-pdb created

# Verify the deployment was created
kubectl get deployment nginx-auto-pdb

# Verify the PDB was auto-created by the mutating webhook
kubectl get pdb nginx-auto-pdb

# Check PDB details (should have minAvailable: 2, maxUnavailable: 4)
kubectl describe pdb nginx-auto-pdb
```

### Test 2: Manual PDB (Explicit)

```bash
# This deployment has an explicitly defined PDB
kubectl apply -f test/deployment-with-pdb.yaml

# Expected success:
# poddisruptionbudget.policy/nginx-with-pdb created
# deployment.apps/nginx-with-pdb created

# Verify both resources were created
kubectl get pdb nginx-with-pdb
kubectl get deployment nginx-with-pdb
kubectl get pods -l app=nginx-with-pdb
```

### Test 3: Manual PDB without Matching Selector

```bash
# Create a deployment with a PDB that doesn't match
kubectl apply -f - <<EOF
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: wrong-pdb
  namespace: default
spec:
  minAvailable: 1
  selector:
    matchLabels:
      app: different-app
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: nginx-wrong-labels
  namespace: default
spec:
  replicas: 1
  selector:
    matchLabels:
      app: nginx-wrong-labels
  template:
    metadata:
      labels:
        app: nginx-wrong-labels
    spec:
      containers:
        - name: nginx
          image: nginx:1.26-alpine
EOF

# Expected: REJECTED by validating webhook
# Error: deployment rejected: no PodDisruptionBudget in namespace default selects pod labels...
# The PDB "wrong-pdb" selects app: different-app, not app: nginx-wrong-labels

# Verify the deployment was NOT created
kubectl get deployment nginx-wrong-labels 2>&1 | grep -i "not found"
```

### Test 4: Namespace with Auto-creation Enabled

```bash
# Create a namespace with PDB auto-creation labels
kubectl create namespace staging
kubectl label namespace staging \
  pdb-webhook.awanipro.com/min-available=1 \
  pdb-webhook.awanipro.com/max-unavailable=2

# Create a deployment in staging - should get auto-created PDB
kubectl apply -f - <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: app-staging
  namespace: staging
spec:
  replicas: 3
  selector:
    matchLabels:
      app: app-staging
  template:
    metadata:
      labels:
        app: app-staging
    spec:
      containers:
        - name: app
          image: nginx:1.26-alpine
EOF

# Verify PDB was auto-created with namespace-configured values
kubectl describe pdb app-staging -n staging
# Should show: Min Available: 1, Max Unavailable: 2
```

### Test 5: Namespace WITHOUT Auto-creation

```bash
# Create a namespace WITHOUT PDB auto-creation labels
kubectl create namespace no-auto

# Try to deploy without explicit PDB - should be REJECTED
kubectl apply -f - <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: app-no-auto
  namespace: no-auto
spec:
  replicas: 3
  selector:
    matchLabels:
      app: app-no-auto
  template:
    metadata:
      labels:
        app: app-no-auto
    spec:
      containers:
        - name: app
          image: nginx:1.26-alpine
EOF

# Expected: REJECTED by validating webhook
# Error: deployment rejected: no PodDisruptionBudget in namespace no-auto...

# Now try with explicit PDB - should SUCCEED
kubectl apply -f - <<EOF
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: app-no-auto
  namespace: no-auto
spec:
  minAvailable: 1
  selector:
    matchLabels:
      app: app-no-auto
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: app-no-auto
  namespace: no-auto
spec:
  replicas: 3
  selector:
    matchLabels:
      app: app-no-auto
  template:
    metadata:
      labels:
        app: app-no-auto
    spec:
      containers:
        - name: app
          image: nginx:1.26-alpine
EOF

# Should succeed - explicit PDB satisfies the requirement
```

### Test 5: Webhook Failure Scenarios

```bash
# If mutating webhook fails (failurePolicy: Ignore):
# - Deployment proceeds without auto-created PDB
# - Validating webhook will catch it and reject it

# If validating webhook fails (failurePolicy: Fail):
# - Deployment is rejected (strict enforcement)
# - Ensure webhook is highly available

# Monitor webhook health
kubectl get pods -n webhook-system -l app.kubernetes.io/name=pdb-webhook
kubectl logs -n webhook-system -l app.kubernetes.io/name=pdb-webhook --follow
```

## Step 6: Cleanup

### Remove test deployments

```bash
kubectl delete deployment nginx-with-pdb nginx-without-pdb
kubectl delete pdb nginx-with-pdb
```

### Remove webhook (reverse order)

```bash
# FIRST: Remove the webhook configuration
kubectl delete validatingwebhookconfigurations pdb-webhook

# THEN: Remove the workload
kubectl delete deployment pdb-webhook -n webhook-system
kubectl delete service pdb-webhook -n webhook-system

# THEN: Remove TLS certificate
kubectl delete certificate pdb-webhook-tls -n webhook-system
kubectl delete issuer pdb-webhook-selfsigned -n webhook-system

# THEN: Remove RBAC
kubectl delete clusterrolebinding pdb-webhook
kubectl delete clusterrole pdb-webhook

# FINALLY: Remove namespace (ServiceAccount goes with it)
kubectl delete namespace webhook-system
```

## Troubleshooting

### Webhook not registered

Check if the `ValidatingWebhookConfiguration` has an empty `caBundle`:

```bash
kubectl get validatingwebhookconfigurations pdb-webhook -o yaml | grep caBundle
```

If empty, cert-manager may not have injected it. Ensure:
1. `cert-manager` is installed
2. Certificate is ready: `kubectl get certificate -n webhook-system`
3. Re-apply the webhook config: `kubectl apply -f manifests/validatingwebhookconfiguration.yaml`

### Webhook not responding

Check pod logs:

```bash
kubectl logs -n webhook-system -l app.kubernetes.io/name=pdb-webhook
```

Check pod is running:

```bash
kubectl get pods -n webhook-system -l app.kubernetes.io/name=pdb-webhook
```

Check service connectivity:

```bash
kubectl get svc pdb-webhook -n webhook-system
```

### TLS certificate issues

Verify the certificate is valid:

```bash
kubectl get certificate pdb-webhook-tls -n webhook-system -o yaml
```

Check secret contents:

```bash
kubectl get secret pdb-webhook-tls -n webhook-system -o yaml | grep -A 2 tls.crt
```

### Deployment rejection when it shouldn't be

Check webhook logs to see what labels it's matching against:

```bash
kubectl logs -n webhook-system -l app.kubernetes.io/name=pdb-webhook | grep "validating deployment"
```

Ensure the PDB selector matches the deployment's pod template labels exactly.

## Operational Considerations

### High Availability

- Deployment has 2 replicas by default with pod anti-affinity
- For production, consider increasing replicas and enabling pod disruption budgets for the webhook itself

### Monitoring

Monitor the following:

```bash
# Check webhook API latency
kubectl top pod -n webhook-system -l app.kubernetes.io/name=pdb-webhook

# Watch for webhook errors in logs
kubectl logs -n webhook-system -l app.kubernetes.io/name=pdb-webhook --all-containers | grep -i error

# Monitor webhook availability (failurePolicy: Fail means unavailability = admission denial)
kubectl get validatingwebhookconfigurations pdb-webhook
```

### Failure Policy

The webhook uses `failurePolicy: Fail`, which means:
- If the webhook is unreachable, the admission request is **DENIED**
- This enforces PDB compliance but requires webhook reliability

Consider changing to `failurePolicy: Ignore` (less strict) if high availability is not feasible.

### Capacity and Timeouts

- Timeout is set to 10 seconds
- Webhook server is configured to parse up to 1 MiB AdmissionReview requests
- Resource requests/limits are conservative; adjust based on load testing

## Additional References

- [Kubernetes Admission Webhooks](https://kubernetes.io/docs/reference/access-authn-authz/extensible-admission-controllers/)
- [PodDisruptionBudget](https://kubernetes.io/docs/tasks/run-application/configure-pdb/)
- [cert-manager Certificate](https://cert-manager.io/docs/concepts/certificate/)
