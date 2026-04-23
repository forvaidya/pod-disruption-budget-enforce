#!/bin/bash

# PDB Admission Webhook - Cleanup Script
# Removes all webhook components from cluster

set -e

echo "================================================"
echo "PDB Admission Webhook - Cleanup"
echo "================================================"
echo ""

# Step 1: Remove webhook configurations
echo "Step 1: Removing webhook configurations..."
kubectl delete validatingwebhookconfigurations pdb-webhook 2>/dev/null || true
kubectl delete mutatingwebhookconfigurations pdb-webhook 2>/dev/null || true
echo "✓ Webhook configs removed"
echo ""

# Step 2: Remove workload
echo "Step 2: Removing webhook deployment..."
kubectl delete deployment pdb-webhook -n webhook-system 2>/dev/null || true
kubectl delete service pdb-webhook -n webhook-system 2>/dev/null || true
echo "✓ Deployment removed"
echo ""

# Step 3: Remove TLS
echo "Step 3: Removing TLS certificate..."
kubectl delete secret pdb-webhook-tls -n webhook-system 2>/dev/null || true
echo "✓ TLS secret removed"
echo ""

# Step 4: Remove RBAC
echo "Step 4: Removing RBAC..."
kubectl delete clusterrolebinding pdb-webhook 2>/dev/null || true
kubectl delete clusterrole pdb-webhook 2>/dev/null || true
echo "✓ RBAC removed"
echo ""

# Step 5: Remove namespace
echo "Step 5: Removing namespace..."
kubectl delete namespace webhook-system 2>/dev/null || true
echo "✓ Namespace removed"
echo ""

# Step 6: Clean up test resources
echo "Step 6: Cleaning up test resources..."
kubectl delete namespace pdb-test 2>/dev/null || true
echo "✓ Test resources removed"
echo ""

echo "================================================"
echo "✅ Cleanup Complete!"
echo "================================================"
