# Namespace PDB Configuration Labels

## New Labels

Namespaces can be labeled with PDB configuration:

```yaml
pdb-webhook-mutate: "true"              # Enable auto-PDB creation
pdb-min-available: "1"                  # Min pods available (for mutating webhook)
pdb-max-unavailable: "2"                # Max pods unavailable (for mutating webhook)
```

## How It Works

When a deployment is created in a namespace with these labels:

1. **Mutating webhook checks**:
   - Is `pdb-webhook-mutate=true`?
   - Does namespace have `pdb-min-available` and `pdb-max-unavailable` labels?

2. **If YES**: Auto-create PDB with:
   ```yaml
   minAvailable: <value from pdb-min-available label>
   maxUnavailable: <value from pdb-max-unavailable label>
   ```

3. **Selector**: Uses deployment's pod template labels

## Setup Examples

### Production with Min=1, Max=2
```bash
kubectl create namespace production
kubectl label namespace production \
  pdb-webhook-mutate=true \
  pdb-min-available=1 \
  pdb-max-unavailable=2
```

### Staging with Min=0, Max=1
```bash
kubectl create namespace staging
kubectl label namespace staging \
  pdb-webhook-mutate=true \
  pdb-min-available=0 \
  pdb-max-unavailable=1
```

### Dev with Min=0, Max=all (lenient)
```bash
kubectl create namespace dev
kubectl label namespace dev \
  pdb-webhook-mutate=true \
  pdb-min-available=0 \
  pdb-max-unavailable=1
```

## Verify Labels

```bash
# View single namespace
kubectl get namespace production --show-labels

# View all with custom columns
kubectl get ns -o custom-columns=\
NAME:.metadata.name,\
MUTATE:.metadata.labels.pdb-webhook-mutate,\
MIN:.metadata.labels.pdb-min-available,\
MAX:.metadata.labels.pdb-max-unavailable
```

## Test It

```bash
# Setup namespace with PDB config
kubectl create namespace test-pdb
kubectl label namespace test-pdb \
  pdb-webhook-mutate=true \
  pdb-min-available=2 \
  pdb-max-unavailable=1

# Deploy without explicit PDB
cat > /tmp/test-deploy.yaml << 'MANIFEST'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: app
  namespace: test-pdb
spec:
  replicas: 3
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
MANIFEST

kubectl apply -f /tmp/test-deploy.yaml

# Check auto-created PDB
kubectl get pdb app -n test-pdb -o yaml
# Should have: minAvailable: 2, maxUnavailable: 1
```

## Update Namespace Configuration

Change the min/max values for a namespace:

```bash
# Update min-available to 2
kubectl label namespace production pdb-min-available=2 --overwrite

# Update max-unavailable to 3
kubectl label namespace production pdb-max-unavailable=3 --overwrite
```

Note: Changing these labels affects only **new** deployments created after the label change.
Existing PDBs won't be updated automatically.

## All Label Combinations

| mutate | validate | min-available | max-unavailable | Behavior |
|--------|----------|---|---|---|
| true | true | 1 | 2 | Auto-create PDB (1,2) + enforce |
| true | false | 1 | 2 | Auto-create PDB (1,2) only |
| true | true | (none) | (none) | No auto-creation, but enforce |
| false | true | - | - | Enforce, manual PDB required |
| false | false | - | - | No protection |

