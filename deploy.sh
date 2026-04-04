#!/bin/bash
set -e

echo "=== PDB Admission Webhook Deployment ==="
echo ""

# Step 1: Install cert-manager
echo "Step 1: Installing cert-manager..."
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.14.0/cert-manager.yaml
echo "Waiting for cert-manager to be ready..."
kubectl wait --for=condition=Available --timeout=300s deployment/cert-manager -n cert-manager 2>/dev/null || true
sleep 5

# Step 2: Deploy RBAC
echo ""
echo "Step 2: Deploying RBAC..."
kubectl apply -f manifests/namespace.yaml
kubectl apply -f manifests/serviceaccount.yaml
kubectl apply -f manifests/clusterrole.yaml
kubectl apply -f manifests/clusterrolebinding.yaml

# Step 3: Deploy TLS certificate
echo ""
echo "Step 3: Creating TLS certificate..."
kubectl apply -f manifests/certificate.yaml
echo "Waiting for certificate to be ready..."
kubectl wait --for=condition=Ready certificate/pdb-webhook-tls -n webhook-system --timeout=60s

# Step 4: Deploy webhook server
echo ""
echo "Step 4: Deploying webhook server..."
kubectl apply -f manifests/deployment.yaml
kubectl apply -f manifests/service.yaml
echo "Waiting for deployment to be ready..."
kubectl wait --for=condition=Available --timeout=60s deployment/pdb-webhook -n webhook-system

# Step 5: Register webhooks (LAST)
echo ""
echo "Step 5: Registering webhooks..."
kubectl apply -f manifests/mutatingwebhookconfiguration.yaml
kubectl apply -f manifests/validatingwebhookconfiguration.yaml

echo ""
echo "=== Deployment Complete ==="
echo ""
echo "Checking webhook status..."
kubectl get pods -n webhook-system -l app.kubernetes.io/name=pdb-webhook
echo ""
echo "Recent logs:"
kubectl logs -n webhook-system -l app.kubernetes.io/name=pdb-webhook --tail=10
echo ""
echo "Next: Run tests with 'kubectl apply -f test/deployment-*.yaml'"
