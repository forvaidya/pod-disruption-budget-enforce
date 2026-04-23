# Quick Start - PDB Admission Webhook

Complete end-to-end setup in a single command!

## Prerequisites

- Kubernetes cluster (1.24+) running and accessible via `kubectl`
- Docker installed (for building the image)
- `openssl` installed (for certificate generation)

## One-Command Setup

```bash
./setup.sh
```

This automatically:
1. ✅ Builds the Docker image
2. ✅ Generates TLS certificates with proper SANs
3. ✅ Creates namespace and RBAC
4. ✅ Deploys webhook server (4 replicas)
5. ✅ Registers mutating and validating webhooks
6. ✅ Tests the system end-to-end
7. ✅ Creates a test namespace with auto-PDB enabled

### With custom configuration

```bash
./setup.sh webhook-system 3 1
```

Arguments:
- `webhook-system` - Namespace for webhook (default: webhook-system)
- `3` - Default min-available for auto-created PDBs (default: 2)
- `1` - Default max-unavailable for auto-created PDBs (default: 4)

## Usage After Setup

### Enable auto-PDB in any namespace

```bash
kubectl label namespace my-app \
  pdb-webhook.awanipro.com/min-available=2 \
  pdb-webhook.awanipro.com/max-unavailable=4
```

### Deploy your application

```bash
kubectl apply -f my-deployment.yaml -n my-app
```

The mutating webhook will automatically create a PDB for you!

### Verify PDB was created

```bash
kubectl get pdb -n my-app
kubectl describe pdb <deployment-name> -n my-app
```

## Cleanup

Remove all webhook components:

```bash
./cleanup.sh
```

## What Gets Deployed

```
webhook-system/
├── Deployment: pdb-webhook (4 replicas)
├── Service: pdb-webhook (443 → 8443)
├── Secret: pdb-webhook-tls (TLS certificates)
├── ServiceAccount: pdb-webhook
└── ClusterRole: pdb-webhook (read PDBs)

Cluster-wide:
├── MutatingWebhookConfiguration: pdb-webhook
└── ValidatingWebhookConfiguration: pdb-webhook
```

## Verify Setup

```bash
# Check pods
kubectl get pods -n webhook-system

# Check webhooks
kubectl get mutatingwebhookconfigurations pdb-webhook
kubectl get validatingwebhookconfigurations pdb-webhook

# View logs
kubectl logs -n webhook-system -l app.kubernetes.io/name=pdb-webhook --follow
```

## How It Works

### Opt-in Enforcement
- Namespaces **without** PDB labels: ✅ Allowed (no enforcement)
- Namespaces **with** PDB labels: ✅ Auto-creates PDB, ❌ Rejects deployments without matching PDB
- Incomplete config (only one label): ❌ Rejected

### Webhook Sequence
1. **Mutating Webhook** (runs first)
   - Checks for `pdb-webhook.awanipro.com/min-available` and `pdb-webhook.awanipro.com/max-unavailable` labels
   - If both present: Auto-creates PDB with those values
   - If incomplete: Rejects deployment
   - If missing: Allows deployment

2. **Validating Webhook** (runs second)
   - Ensures deployment has matching PDB
   - Acts as safety net for edge cases
   - Prevents label removal from namespaces

## Troubleshooting

### Webhooks not working?

Check pod status:
```bash
kubectl get pods -n webhook-system -l app.kubernetes.io/name=pdb-webhook
kubectl logs -n webhook-system -l app.kubernetes.io/name=pdb-webhook
```

Check webhook registration:
```bash
kubectl describe mutatingwebhookconfigurations pdb-webhook
kubectl describe validatingwebhookconfigurations pdb-webhook
```

### Deployment rejected?

Check the error message:
```bash
kubectl apply -f my-deployment.yaml 2>&1
```

Common issues:
- **"incomplete PDB configuration"** → Add both labels to namespace
- **"no PodDisruptionBudget"** → Namespace has labels but no matching PDB

### Certificate issues?

Verify CA bundle is injected:
```bash
kubectl get validatingwebhookconfigurations pdb-webhook -o yaml | grep -A 1 caBundle
```

## Architecture

```
Deployment CREATE Request
        ↓
┌───────────────────────────────────────┐
│  MUTATING WEBHOOK (pdb-webhook-mutate)│
│  ────────────────────────────────────│
│  1. Check namespace labels            │
│  2. Auto-create PDB if enabled        │
│  3. Reject if incomplete config       │
└──────────┬──────────────────────────┘
           ↓
┌───────────────────────────────────────┐
│ VALIDATING WEBHOOK (pdb-webhook)      │
│ ────────────────────────────────────  │
│ 1. Enforce PDB exists (if configured) │
│ 2. Allow or reject                    │
└──────────┬──────────────────────────┘
           ↓
      ✅ or ❌ Admission Response
```

## Performance

- **Webhook timeout**: 10 seconds
- **Replicas**: 4 with pod anti-affinity
- **Image size**: ~10 MB (distroless base)
- **Memory**: 128Mi requests, 256Mi limits per pod

## Security

- ✅ TLS encryption (self-signed with SANs)
- ✅ Non-root containers (UID 65532)
- ✅ Read-only filesystems
- ✅ No Linux capabilities
- ✅ RBAC: Minimal permissions (read PDBs only)
- ✅ Excluded namespaces: webhook-system, kube-system

## References

- [DEPLOYMENT.md](./DEPLOYMENT.md) - Detailed deployment guide
- [IMPLEMENTATION_SUMMARY.md](./IMPLEMENTATION_SUMMARY.md) - Architecture overview
- [CLAUDE.md](./CLAUDE.md) - Project specifications
