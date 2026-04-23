#!/bin/bash

# PDB Admission Webhook - Complete Setup Script
# Usage: ./setup.sh [namespace] [min-available] [max-unavailable]
# Default: ./setup.sh webhook-system 2 4

set -e

WEBHOOK_NS="${1:-webhook-system}"
MIN_AVAILABLE="${2:-2}"
MAX_UNAVAILABLE="${3:-4}"
CERTS_DIR="/tmp/pdb-webhook-certs"

echo "================================================"
echo "PDB Admission Webhook - Complete Setup"
echo "================================================"
echo "Webhook Namespace: $WEBHOOK_NS"
echo "Default PDB Config: min=$MIN_AVAILABLE, max=$MAX_UNAVAILABLE"
echo ""

# Step 1: Build Docker image
echo "Step 1: Building Docker image..."
docker build -t pdb-webhook:latest .
echo "✓ Image built"
echo ""

# Step 2: Generate TLS certificates with SANs
echo "Step 2: Generating TLS certificates with SANs..."
mkdir -p "$CERTS_DIR"
cd "$CERTS_DIR"

# Generate CA
openssl genrsa -out ca.key 2048 2>/dev/null
openssl req -x509 -new -nodes -key ca.key -days 365 -out ca.crt \
  -subj "/CN=pdb-webhook-ca" 2>/dev/null

# Generate webhook key
openssl genrsa -out webhook.key 2048 2>/dev/null

# Create config with SANs
cat > webhook.conf << 'CONF'
[req]
default_bits       = 2048
prompt              = no
default_md          = sha256
distinguished_name  = req_distinguished_name
req_extensions      = v3_req

[req_distinguished_name]
CN = pdb-webhook.webhook-system.svc

[v3_req]
subjectAltName = DNS:pdb-webhook,DNS:pdb-webhook.webhook-system,DNS:pdb-webhook.webhook-system.svc,DNS:pdb-webhook.webhook-system.svc.cluster.local
CONF

# Generate CSR and sign
openssl req -new -key webhook.key -out webhook.csr -config webhook.conf 2>/dev/null
openssl x509 -req -days 365 -in webhook.csr \
  -CA ca.crt -CAkey ca.key -CAcreateserial \
  -out webhook.crt \
  -extensions v3_req -extfile webhook.conf 2>/dev/null

echo "✓ Certificates generated in $CERTS_DIR"
cd - > /dev/null
echo ""

# Step 3: Create namespace and RBAC
echo "Step 3: Creating namespace and RBAC..."
kubectl apply -f manifests/namespace.yaml
kubectl apply -f manifests/serviceaccount.yaml
kubectl apply -f manifests/clusterrole.yaml
kubectl apply -f manifests/clusterrolebinding.yaml
echo "✓ Namespace and RBAC created"
echo ""

# Step 4: Create TLS Secret
echo "Step 4: Creating TLS Secret..."
kubectl create secret tls pdb-webhook-tls \
  --cert="$CERTS_DIR/webhook.crt" \
  --key="$CERTS_DIR/webhook.key" \
  -n webhook-system 2>/dev/null || \
kubectl create secret tls pdb-webhook-tls \
  --cert="$CERTS_DIR/webhook.crt" \
  --key="$CERTS_DIR/webhook.key" \
  -n webhook-system --dry-run=client -o yaml | kubectl apply -f -
echo "✓ TLS Secret created"
echo ""

# Step 5: Deploy webhook server
echo "Step 5: Deploying webhook server..."
kubectl apply -f manifests/deployment.yaml
kubectl apply -f manifests/service.yaml

echo "Waiting for webhook pods to be ready..."
kubectl wait --for=condition=Ready pod \
  -l app.kubernetes.io/name=pdb-webhook \
  -n webhook-system --timeout=60s 2>/dev/null || true

echo "✓ Webhook server deployed"
echo ""

# Step 6: Create webhook configurations with CA bundle
echo "Step 6: Registering webhook configurations..."
CA_BUNDLE=$(base64 -i "$CERTS_DIR/ca.crt" | tr -d '\n')

# Mutating webhook
cat > /tmp/mutating-webhook.yaml << EOF
apiVersion: admissionregistration.k8s.io/v1
kind: MutatingWebhookConfiguration
metadata:
  name: pdb-webhook
  labels:
    app.kubernetes.io/name: pdb-webhook
    app.kubernetes.io/component: admission-controller
webhooks:
  - name: pdb-webhook-mutate.webhook-system.svc
    admissionReviewVersions: ["v1"]
    clientConfig:
      service:
        name: pdb-webhook
        namespace: webhook-system
        path: /mutate
        port: 443
      caBundle: $CA_BUNDLE
    rules:
      - operations: ["CREATE", "UPDATE"]
        apiGroups: [""]
        apiVersions: ["v1"]
        resources: ["namespaces"]
        scope: "Cluster"
      - operations: ["CREATE"]
        apiGroups: ["apps"]
        apiVersions: ["v1"]
        resources: ["deployments", "statefulsets"]
        scope: "*"
    failurePolicy: Fail
    sideEffects: None
    timeoutSeconds: 10
EOF

# Validating webhook
cat > /tmp/validating-webhook.yaml << EOF
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingWebhookConfiguration
metadata:
  name: pdb-webhook
  labels:
    app.kubernetes.io/name: pdb-webhook
    app.kubernetes.io/component: admission-controller
webhooks:
  - name: pdb-webhook.webhook-system.svc
    admissionReviewVersions: ["v1"]
    clientConfig:
      service:
        name: pdb-webhook
        namespace: webhook-system
        path: /validate
        port: 443
      caBundle: $CA_BUNDLE
    rules:
      - operations: ["CREATE", "UPDATE"]
        apiGroups: ["apps"]
        apiVersions: ["v1"]
        resources: ["deployments", "statefulsets", "pods"]
        scope: "*"
      - operations: ["UPDATE"]
        apiGroups: [""]
        apiVersions: ["v1"]
        resources: ["namespaces"]
        scope: "Cluster"
    failurePolicy: Fail
    sideEffects: None
    timeoutSeconds: 10
    namespaceSelector:
      matchExpressions:
        - key: kubernetes.io/metadata.name
          operator: NotIn
          values:
            - webhook-system
            - kube-system
EOF

kubectl apply -f /tmp/mutating-webhook.yaml
kubectl apply -f /tmp/validating-webhook.yaml

echo "✓ Webhooks registered"
echo ""

# Step 7: Test setup
echo "Step 7: Testing webhook system..."
echo ""

# Create test namespace
kubectl create namespace pdb-test 2>/dev/null || true
kubectl label namespace pdb-test \
  pdb-webhook.awanipro.com/min-available=$MIN_AVAILABLE \
  pdb-webhook.awanipro.com/max-unavailable=$MAX_UNAVAILABLE \
  --overwrite 2>/dev/null || true

# Deploy test workload
kubectl apply -f - -n pdb-test << 'YAML'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: test-app
spec:
  replicas: 2
  selector:
    matchLabels:
      app: test-app
  template:
    metadata:
      labels:
        app: test-app
    spec:
      containers:
        - name: nginx
          image: nginx:1.26-alpine
YAML

sleep 2

# Verify PDB was auto-created
if kubectl get pdb test-app -n pdb-test &>/dev/null; then
  echo "✓ TEST PASSED: PDB auto-created with:"
  kubectl describe pdb test-app -n pdb-test | grep -E "Min|Max|Selector" | head -3
else
  echo "❌ TEST FAILED: PDB was not auto-created"
  exit 1
fi

echo ""
echo "================================================"
echo "✅ Setup Complete!"
echo "================================================"
echo ""
echo "Webhook Status:"
kubectl get pods -n webhook-system -l app.kubernetes.io/name=pdb-webhook
echo ""
echo "Webhook Configurations:"
echo "  Mutating: $(kubectl get mutatingwebhookconfigurations pdb-webhook -o jsonpath='{.webhooks[0].name}' 2>/dev/null || echo 'ERROR')"
echo "  Validating: $(kubectl get validatingwebhookconfigurations pdb-webhook -o jsonpath='{.webhooks[0].name}' 2>/dev/null || echo 'ERROR')"
echo ""
echo "To enable auto-PDB creation in a namespace:"
echo "  kubectl label namespace <name> \\"
echo "    pdb-webhook.awanipro.com/min-available=N \\"
echo "    pdb-webhook.awanipro.com/max-unavailable=M"
echo ""
echo "Test namespace configured: pdb-test"
echo "To clean up test: kubectl delete namespace pdb-test"
