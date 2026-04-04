#!/bin/bash

echo "=========================================="
echo "Test: Enforcement vs Non-Enforced Namespace"
echo "=========================================="
echo ""

# Create two namespaces
echo "Step 1: Create two test namespaces"
echo "=================================="
kubectl create namespace enforced-ns 2>/dev/null || true
kubectl create namespace non-enforced-ns 2>/dev/null || true
echo "✓ Namespaces created"
echo ""

# Label only one namespace for enforcement
echo "Step 2: Apply labels"
echo "===================="
echo "Enforced NS: pdb-webhook-validate=true"
kubectl label namespace enforced-ns pdb-webhook-validate=true --overwrite
echo "Non-Enforced NS: NO labels"
kubectl label namespace non-enforced-ns pdb-webhook-validate- pdb-webhook-mutate- --overwrite 2>/dev/null || true

echo ""
echo "Namespace labels:"
echo "- enforced-ns:"
kubectl get ns enforced-ns -o jsonpath='{.metadata.labels}' | jq .
echo "- non-enforced-ns:"
kubectl get ns non-enforced-ns -o jsonpath='{.metadata.labels}' | jq .
echo ""

# Create deployment without PDB for enforced namespace
echo "Step 3: Test ENFORCED namespace (pdb-webhook-validate=true)"
echo "==========================================================="
cat > /tmp/deploy-enforced.yaml << 'MANIFEST'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: test-no-pdb
  namespace: enforced-ns
spec:
  replicas: 2
  selector:
    matchLabels:
      app: test-enforced
  template:
    metadata:
      labels:
        app: test-enforced
    spec:
      containers:
        - name: nginx
          image: nginx:1.26-alpine
MANIFEST

echo "Applying deployment WITHOUT PDB to enforced-ns..."
kubectl apply -f /tmp/deploy-enforced.yaml 2>&1 | tail -3
echo ""

if kubectl get deployment test-no-pdb -n enforced-ns 2>/dev/null > /dev/null; then
  echo "❌ FAIL: Deployment created (should be REJECTED)"
else
  echo "✅ PASS: Deployment REJECTED (no matching PDB)"
fi
echo ""

# Create deployment without PDB for non-enforced namespace
echo "Step 4: Test NON-ENFORCED namespace (no labels)"
echo "=============================================="
cat > /tmp/deploy-non-enforced.yaml << 'MANIFEST'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: test-no-pdb
  namespace: non-enforced-ns
spec:
  replicas: 2
  selector:
    matchLabels:
      app: test-non-enforced
  template:
    metadata:
      labels:
        app: test-non-enforced
    spec:
      containers:
        - name: nginx
          image: nginx:1.26-alpine
MANIFEST

echo "Applying deployment WITHOUT PDB to non-enforced-ns..."
kubectl apply -f /tmp/deploy-non-enforced.yaml 2>&1 | tail -3
echo ""

if kubectl get deployment test-no-pdb -n non-enforced-ns 2>/dev/null > /dev/null; then
  echo "✅ PASS: Deployment ALLOWED (no enforcement)"
else
  echo "❌ FAIL: Deployment rejected (should be allowed)"
fi
echo ""

# Check if PDB was auto-created
echo "Step 5: Verify NO modification in non-enforced namespace"
echo "======================================================"
if kubectl get pdb test-no-pdb -n non-enforced-ns 2>/dev/null > /dev/null; then
  echo "❌ FAIL: PDB auto-created (webhook should not have run)"
else
  echo "✅ PASS: NO PDB created (webhook was not invoked)"
fi
echo ""

# Final comparison
echo "Step 6: Side-by-Side Comparison"
echo "==============================="
echo ""
echo "ENFORCED NAMESPACE (pdb-webhook-validate=true):"
echo "  Deployments: $(kubectl get deploy -n enforced-ns --no-headers 2>/dev/null | wc -l) (expected: 0)"
echo "  PDBs: $(kubectl get pdb -n enforced-ns --no-headers 2>/dev/null | wc -l) (expected: 0)"
echo ""
echo "NON-ENFORCED NAMESPACE (no labels):"
echo "  Deployments: $(kubectl get deploy -n non-enforced-ns --no-headers 2>/dev/null | wc -l) (expected: 1)"
echo "  PDBs: $(kubectl get pdb -n non-enforced-ns --no-headers 2>/dev/null | wc -l) (expected: 0)"
echo ""

# Cleanup
echo "Step 7: Cleanup"
echo "=============="
kubectl delete namespace enforced-ns non-enforced-ns --ignore-not-found=true
echo "✓ Test namespaces deleted"
echo ""
echo "=========================================="
echo "Test Complete"
echo "=========================================="
