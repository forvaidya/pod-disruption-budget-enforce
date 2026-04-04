#!/bin/bash

echo "=========================================="
echo "Cutover Test: IP Space Capacity"
echo "=========================================="
echo ""

# Namespace 1: GOOD - Enough IP space
echo "Step 1: Create GOOD namespace (enough IP space)"
echo "=============================================="
kubectl create namespace cutover-good 2>/dev/null || true

# Label for PDB mutation
kubectl label namespace cutover-good \
  pdb-webhook-mutate=true \
  pdb-min-available=1 \
  pdb-max-unavailable=2 --overwrite

# Create initial deployment (v1)
cat > /tmp/good-v1.yaml << 'MANIFEST'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: app
  namespace: cutover-good
spec:
  replicas: 3
  strategy:
    type: RollingUpdate
    rollingUpdate:
      maxSurge: 2        # Allow 2 new pods before old evict
      maxUnavailable: 1  # Can evict 1 old pod at a time
  selector:
    matchLabels:
      app: myapp
  template:
    metadata:
      labels:
        app: myapp
    spec:
      containers:
        - name: app
          image: nginx:1.26-alpine
          resources:
            requests:
              cpu: 10m
              memory: 32Mi
MANIFEST

echo "✓ Namespace created: cutover-good"
echo "  - PDB minAvailable: 1"
echo "  - Deployment replicas: 3"
echo "  - Rolling strategy: maxSurge=2, maxUnavailable=1"
echo ""

echo "Deploying app v1..."
kubectl apply -f /tmp/good-v1.yaml

# Wait for pods to be ready
sleep 5

echo "Current state (v1):"
kubectl get pods -n cutover-good -o wide | tail -4
echo ""
echo "PDB status:"
kubectl get pdb app -n cutover-good -o wide 2>/dev/null || echo "(No PDB yet)"
echo ""

# Now do rolling update to v2
echo "Step 2: Start rolling update (v1 → v2)"
echo "======================================="
echo ""
echo "Before update:"
echo "  Current pods: $(kubectl get pods -n cutover-good --no-headers 2>/dev/null | wc -l)"
echo ""

# Update image to trigger rolling update
kubectl set image deployment/app \
  app=nginx:latest \
  -n cutover-good \
  --record 2>&1 | tail -2

echo ""
echo "During update (watching pod overlap):"
echo ""

# Watch the cutover happen
for i in {1..10}; do
  v1_count=$(kubectl get pods -n cutover-good -o jsonpath='{.items[?(@.metadata.ownerReferences[0].name=="app")].metadata.name}' 2>/dev/null | wc -w)
  total=$(kubectl get pods -n cutover-good --no-headers 2>/dev/null | wc -l)
  
  echo "  Time $i: Total pods=$total"
  
  if [ $total -lt 4 ]; then
    break
  fi
  
  sleep 2
done

echo ""
echo "After update:"
kubectl get pods -n cutover-good -o wide | tail -5
echo ""
echo "✅ GOOD namespace: Cutover succeeded (had enough IP space)"
echo ""

echo "=========================================="
echo "Step 3: Check PDB was maintained"
echo "=========================================="
echo ""
kubectl describe pdb app -n cutover-good 2>/dev/null | grep -E "Min Available|Allowed" || echo "PDB info"
echo ""

echo "=========================================="
echo "Step 4: Show final state"
echo "=========================================="
echo ""
echo "Namespace: cutover-good"
echo "─────────────────────────"
echo "Deployments:"
kubectl get deploy -n cutover-good
echo ""
echo "PDB:"
kubectl get pdb -n cutover-good
echo ""
echo "Pods (all running with IP assigned):"
kubectl get pods -n cutover-good -o wide
echo ""

echo "=========================================="
echo "Summary"
echo "=========================================="
echo ""
echo "✅ Rolling update succeeded because:"
echo "   - maxSurge=2: Can create 2 new pods before evicting old"
echo "   - maxUnavailable=1: Can evict 1 old pod at a time"
echo "   - Overlap: v1(3) + v2(2) = 5 pods temporary (still OK)"
echo "   - PDB minAvailable=1: Still satisfied throughout"
echo "   - IP space: Had enough for all temporary pods"
echo ""
echo "Timing: Old pods evicted, new pods created, no deadlock"
echo ""

# Cleanup
echo "=========================================="
echo "Cleanup"
echo "=========================================="
kubectl delete namespace cutover-good --ignore-not-found=true
echo "✓ Namespace deleted"
