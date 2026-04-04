#!/bin/bash
set -e

echo "=== Testing Label-Based Enforcement Semantic ==="
echo ""

# Helper function to test deployment
test_deployment() {
    local ns=$1
    local label=$2
    local expected=$3

    echo "Testing: namespace=$ns, label=$label, expected=$expected"

    cat > /tmp/test-deploy.yaml <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: test-app
  namespace: $ns
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
      - name: app
        image: nginx:latest
EOF

    # Apply and capture result
    if kubectl apply -f /tmp/test-deploy.yaml 2>&1 | grep -q "rejected\|invalid"; then
        echo "  Result: REJECTED"
    else
        echo "  Result: ALLOWED"
    fi
    echo ""
}

# Scenario 1: Both labels present
echo "--- Scenario 1: Both labels present (Enforcement Enabled) ---"
kubectl create namespace test-both --dry-run=client -o yaml | kubectl apply -f -
kubectl label namespace test-both \
    pdb-min-available=2 \
    pdb-max-unavailable=1 \
    --overwrite

echo "Namespace test-both labels:"
kubectl get ns test-both -o jsonpath='{.metadata.labels}'
echo ""
echo ""

test_deployment "test-both" "both" "ALLOWED (with auto-created PDB)"

# Scenario 2: Only min label (Config error)
echo "--- Scenario 2: Only min label (Config Error) ---"
kubectl create namespace test-min-only --dry-run=client -o yaml | kubectl apply -f -
kubectl label namespace test-min-only pdb-min-available=2

echo "Namespace test-min-only labels:"
kubectl get ns test-min-only -o jsonpath='{.metadata.labels}'
echo ""
echo ""

test_deployment "test-min-only" "min-only" "REJECTED (incomplete config)"

# Scenario 3: Only max label (Config error)
echo "--- Scenario 3: Only max label (Config Error) ---"
kubectl create namespace test-max-only --dry-run=client -o yaml | kubectl apply -f -
kubectl label namespace test-max-only pdb-max-unavailable=1

echo "Namespace test-max-only labels:"
kubectl get ns test-max-only -o jsonpath='{.metadata.labels}'
echo ""
echo ""

test_deployment "test-max-only" "max-only" "REJECTED (incomplete config)"

# Scenario 4: No labels (No enforcement)
echo "--- Scenario 4: No labels (No Enforcement) ---"
kubectl create namespace test-no-labels --dry-run=client -o yaml | kubectl apply -f -

echo "Namespace test-no-labels labels:"
kubectl get ns test-no-labels -o jsonpath='{.metadata.labels}' || echo "(no labels)"
echo ""
echo ""

test_deployment "test-no-labels" "none" "ALLOWED (no enforcement)"

echo ""
echo "=== Test Summary ==="
echo "Scenario 1 (both labels): Deployment should be ALLOWED with auto-created PDB"
echo "Scenario 2 (min only):    Deployment should be REJECTED (incomplete config)"
echo "Scenario 3 (max only):    Deployment should be REJECTED (incomplete config)"
echo "Scenario 4 (no labels):   Deployment should be ALLOWED (no enforcement)"
echo ""
echo "Check webhook logs:"
echo "kubectl logs -n webhook-system -l app.kubernetes.io/name=pdb-webhook -f"
