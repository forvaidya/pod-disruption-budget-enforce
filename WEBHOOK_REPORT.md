# PDB Admission Webhook - Deployment Report

## Namespace Architecture

### webhook-system (Primary)
- **Purpose**: Isolated namespace for all webhook infrastructure
- **Status**: Active
- **Resources**: ServiceAccount, Deployment, Service, RBAC bindings

### cert-manager (Dependencies)
- **Purpose**: Certificate management and TLS secret injection
- **Status**: Active
- **Version**: v1.14.0

### default (Testing)
- **Purpose**: Test deployments and PDB validation
- **Status**: Ready for testing
- **Labels**: `pdb-webhook.awanipro.com/min-available=1` (for auto-PDB tests)

---

## Deployment Status

### Pod Status
NAME                           READY   STATUS    RESTARTS   AGE   IP               NODE       NOMINATED NODE   READINESS GATES
pdb-webhook-6cff8f98b5-phk7h   1/1     Running   0          77s   192.168.194.55   orbstack   <none>           <none>
pdb-webhook-6cff8f98b5-t8j2c   1/1     Running   0          77s   192.168.194.54   orbstack   <none>           <none>

### Service Configuration
NAME          TYPE        CLUSTER-IP        EXTERNAL-IP   PORT(S)   AGE   SELECTOR
pdb-webhook   ClusterIP   192.168.194.166   <none>        443/TCP   77s   app.kubernetes.io/name=pdb-webhook

### TLS Certificate Status
NAME              READY   SECRET            AGE
pdb-webhook-tls   True    pdb-webhook-tls   82s

### Secret Status (TLS Credentials)
NAME              TYPE                DATA   AGE
pdb-webhook-tls   kubernetes.io/tls   3      82s

---

## Webhook Configuration

### ValidatingWebhookConfiguration (pdb-webhook)
webhooks:
- admissionReviewVersions:
  - v1
  clientConfig:
    caBundle: LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSURWakNDQWo2Z0F3SUJBZ0lSQUtHOFFud1VEbEZjMHhBeFczamsxbXN3RFFZSktvWklodmNOQVFFTEJRQXcKS1RFbk1DVUdBMVVFQXhNZWNHUmlMWGRsWW1odmIyc3VkMlZpYUc5dmF5MXplWE4wWlcwdWMzWmpNQjRYRFRJMgpNRFF3TkRBNE1UY3dNRm9YRFRJMk1EY3dNekE0TVRjd01Gb3dLVEVuTUNVR0ExVUVBeE1lY0dSaUxYZGxZbWh2CmIyc3VkMlZpYUc5dmF5MXplWE4wWlcwdWMzWmpNSUlCSWpBTkJna3Foa2lHOXcwQkFRRUZBQU9DQVE4QU1JSUIKQ2dLQ0FRRUEwcXNPQVhQa2FqQ1ZVQndPVzZoWGdzZE9MeGNCb21iQUZ3Z2Y4cGZXa1ZHWG8rd1VwWC9QQUNiTQpacmVNamROSXN1OUQzUFJleW9tamlaRU9TYitTK0NQN3ZkK1FWakVKOUI3cDduUlRycnVzMkRrSXZ1U2V5Y1VRCnZLakdMbnE4cG9vUFRuRnJsMDRRSWFlQU1mZzFZVzF2NDNFRlVhMGVReGM4ckZEZDcrSFpzdlRMMktRMkxQT2sKb21XdnA0REozOFQxbWpOQUkwWXhYb045blhMendZTjExWmVHMlB6RmxGQzBtZnpaWDYvclJrNEJ2cW9rNUw4UgptTFltdDVlcmFOTHhLR29aS2VWZkZQR2lJcWxBMDR1V0d1TldnOHB2UGxRY0drTm1TdVBkL1M3c2NhK1N0b1ZiCmNQSHV2MXR4MWpnRFdpWGpuUjFzSTk0Z1RRTUJGUUlEQVFBQm8za3dkekFPQmdOVkhROEJBZjhFQkFNQ0JhQXcKREFZRFZSMFRBUUgvQkFJd0FEQlhCZ05WSFJFRVVEQk9naDV3WkdJdGQyVmlhRzl2YXk1M1pXSm9iMjlyTFhONQpjM1JsYlM1emRtT0NMSEJrWWkxM1pXSm9iMjlyTG5kbFltaHZiMnN0YzNsemRHVnRMbk4yWXk1amJIVnpkR1Z5CkxteHZZMkZzTUEwR0NTcUdTSWIzRFFFQkN3VUFBNElCQVFCS0lIeExsUlhNeGFHRnY4TktZL2JVSzRmaUtHWlYKdU1FSUkzWXl1ZkNpd3M3cCtkNWpRZjJNWkMzUitRSU1mU0VMWVVUUTZiMFBGSGFpSVo5T1hPdFZ6dTFCZDNUZQpCRWN6cWZaTDV3QTZxMUpsbzc0NEx0QWNpWjBlVGpDbVNxS2tOM0VoZVBtNkVBMWMzbGNSTXNhQVIvZTRVRnhaClJXMWFRMk9vWHBwMU41T0dzRzAzSm1uRGdIc2QvU2JVYktLMEhrS2lQQ0hwcEtzUFJFVFFXVDRUYVRmU053QjgKTTRwLzRtUi9ObjBUYVVuTnErckVBc3U4R0N5SUk5Y2daNzcwd0l3ampPSUFEMHRsWk5xUTF4RWdackJCRk44RwpEQkFTenU0RU5xVkxnRnZuSlJheTJzaW53WGJYRmJwWm52Q05ZVW5jV3ZBMjJEOTV2anZLbFJrZAotLS0tLUVORCBDRVJUSUZJQ0FURS0tLS0tCg==
    service:
      name: pdb-webhook
      namespace: webhook-system
      path: /validate
      port: 443
  failurePolicy: Fail
  matchPolicy: Equivalent
  name: pdb-webhook.webhook-system.svc
  namespaceSelector:
    matchExpressions:
    - key: kubernetes.io/metadata.name
      operator: NotIn
      values:
      - webhook-system
    - key: kubernetes.io/metadata.name
      operator: NotIn
      values:
      - kube-system
  objectSelector: {}
  rules:
  - apiGroups:
    - apps
    apiVersions:
    - v1
    operations:
    - CREATE

### MutatingWebhookConfiguration (pdb-webhook-mutate)
webhooks:
- admissionReviewVersions:
  - v1
  clientConfig:
    caBundle: LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSURWakNDQWo2Z0F3SUJBZ0lSQUtHOFFud1VEbEZjMHhBeFczamsxbXN3RFFZSktvWklodmNOQVFFTEJRQXcKS1RFbk1DVUdBMVVFQXhNZWNHUmlMWGRsWW1odmIyc3VkMlZpYUc5dmF5MXplWE4wWlcwdWMzWmpNQjRYRFRJMgpNRFF3TkRBNE1UY3dNRm9YRFRJMk1EY3dNekE0TVRjd01Gb3dLVEVuTUNVR0ExVUVBeE1lY0dSaUxYZGxZbWh2CmIyc3VkMlZpYUc5dmF5MXplWE4wWlcwdWMzWmpNSUlCSWpBTkJna3Foa2lHOXcwQkFRRUZBQU9DQVE4QU1JSUIKQ2dLQ0FRRUEwcXNPQVhQa2FqQ1ZVQndPVzZoWGdzZE9MeGNCb21iQUZ3Z2Y4cGZXa1ZHWG8rd1VwWC9QQUNiTQpacmVNamROSXN1OUQzUFJleW9tamlaRU9TYitTK0NQN3ZkK1FWakVKOUI3cDduUlRycnVzMkRrSXZ1U2V5Y1VRCnZLakdMbnE4cG9vUFRuRnJsMDRRSWFlQU1mZzFZVzF2NDNFRlVhMGVReGM4ckZEZDcrSFpzdlRMMktRMkxQT2sKb21XdnA0REozOFQxbWpOQUkwWXhYb045blhMendZTjExWmVHMlB6RmxGQzBtZnpaWDYvclJrNEJ2cW9rNUw4UgptTFltdDVlcmFOTHhLR29aS2VWZkZQR2lJcWxBMDR1V0d1TldnOHB2UGxRY0drTm1TdVBkL1M3c2NhK1N0b1ZiCmNQSHV2MXR4MWpnRFdpWGpuUjFzSTk0Z1RRTUJGUUlEQVFBQm8za3dkekFPQmdOVkhROEJBZjhFQkFNQ0JhQXcKREFZRFZSMFRBUUgvQkFJd0FEQlhCZ05WSFJFRVVEQk9naDV3WkdJdGQyVmlhRzl2YXk1M1pXSm9iMjlyTFhONQpjM1JsYlM1emRtT0NMSEJrWWkxM1pXSm9iMjlyTG5kbFltaHZiMnN0YzNsemRHVnRMbk4yWXk1amJIVnpkR1Z5CkxteHZZMkZzTUEwR0NTcUdTSWIzRFFFQkN3VUFBNElCQVFCS0lIeExsUlhNeGFHRnY4TktZL2JVSzRmaUtHWlYKdU1FSUkzWXl1ZkNpd3M3cCtkNWpRZjJNWkMzUitRSU1mU0VMWVVUUTZiMFBGSGFpSVo5T1hPdFZ6dTFCZDNUZQpCRWN6cWZaTDV3QTZxMUpsbzc0NEx0QWNpWjBlVGpDbVNxS2tOM0VoZVBtNkVBMWMzbGNSTXNhQVIvZTRVRnhaClJXMWFRMk9vWHBwMU41T0dzRzAzSm1uRGdIc2QvU2JVYktLMEhrS2lQQ0hwcEtzUFJFVFFXVDRUYVRmU053QjgKTTRwLzRtUi9ObjBUYVVuTnErckVBc3U4R0N5SUk5Y2daNzcwd0l3ampPSUFEMHRsWk5xUTF4RWdackJCRk44RwpEQkFTenU0RU5xVkxnRnZuSlJheTJzaW53WGJYRmJwWm52Q05ZVW5jV3ZBMjJEOTV2anZLbFJrZAotLS0tLUVORCBDRVJUSUZJQ0FURS0tLS0tCg==
    service:
      name: pdb-webhook
      namespace: webhook-system
      path: /mutate
      port: 443
  failurePolicy: Ignore
  matchPolicy: Equivalent
  name: pdb-webhook-mutate.webhook-system.svc
  namespaceSelector:
    matchExpressions:
    - key: kubernetes.io/metadata.name
      operator: NotIn
      values:
      - webhook-system
    - key: kubernetes.io/metadata.name
      operator: NotIn
      values:
      - kube-system
  objectSelector: {}
  reinvocationPolicy: Never
  rules:
  - apiGroups:
    - apps
    apiVersions:
    - v1
    operations:

---

## RBAC Configuration

### ServiceAccount
NAME          SECRETS   AGE
pdb-webhook   0         3m12s

### ClusterRole
NAME          CREATED AT
pdb-webhook   2026-04-04T08:15:10Z

### Permissions Detail
rules:
- apiGroups:
  - policy
  resources:
  - poddisruptionbudgets
  verbs:
  - get
  - list
  - watch

---

## Webhook Server Logs (Last 30 lines)

{"level":"info","ts":"2026-04-04T08:17:05Z","msg":"starting pdb-webhook"}
{"level":"info","ts":"2026-04-04T08:17:06Z","msg":"starting TLS server","addr":":8443"}
{"level":"info","ts":"2026-04-04T08:17:05Z","msg":"starting pdb-webhook"}
{"level":"info","ts":"2026-04-04T08:17:06Z","msg":"starting TLS server","addr":":8443"}

---

## Docker Image

pdb-webhook                                                                latest    60ae4da28de8   17 minutes ago   37.2MB

---

## Helm/Manifest Files Used

- manifests/namespace.yaml
- manifests/serviceaccount.yaml
- manifests/clusterrole.yaml
- manifests/clusterrolebinding.yaml
- manifests/certificate.yaml
- manifests/deployment.yaml
- manifests/service.yaml
- manifests/validatingwebhookconfiguration.yaml
- manifests/mutatingwebhookconfiguration.yaml

---

## How It Works

### Validation Flow
1. **User creates/updates Deployment**
2. **API Server invokes MutatingWebhookConfiguration (pdb-webhook-mutate)**
   - Webhook checks if namespace has PDB auto-creation labels
   - If labeled: Auto-creates PDB with configured values (min-available, max-unavailable)
   - If not labeled: Passes through unchanged
3. **API Server invokes ValidatingWebhookConfiguration (pdb-webhook)**
   - Validates that a PDB exists matching deployment's pod labels
   - If no matching PDB found: REJECTS deployment with clear error message
   - If PDB found: ALLOWS deployment

### Failure Policies
- **Mutating**: `failurePolicy: Ignore` (continues even if webhook fails)
- **Validating**: `failurePolicy: Fail` (strict - rejects if webhook unavailable)

---

## Testing Scenarios

### Test 1: Deployment WITH Explicit PDB
```bash
kubectl apply -f test/deployment-with-pdb.yaml
```
**Expected**: ✅ ALLOWED (PDB exists and matches labels)

### Test 2: Deployment WITHOUT PDB
```bash
kubectl apply -f test/deployment-without-pdb.yaml
```
**Expected**: ❌ REJECTED (no matching PDB found)

### Test 3: Auto-PDB Creation
```bash
kubectl label namespace default pdb-webhook.awanipro.com/min-available=1 pdb-webhook.awanipro.com/max-unavailable=2
kubectl apply -f test/deployment-auto-pdb.yaml
```
**Expected**: ✅ ALLOWED (mutating webhook auto-creates PDB)

---

## Troubleshooting Checklist

- [ ] Pods are Running: `kubectl get pods -n webhook-system`
- [ ] Service has endpoints: `kubectl get svc pdb-webhook -n webhook-system`
- [ ] TLS cert is ready: `kubectl get certificate -n webhook-system`
- [ ] caBundle is injected: `kubectl get validatingwebhookconfigurations pdb-webhook -o yaml | grep caBundle`
- [ ] No errors in logs: `kubectl logs -n webhook-system -l app.kubernetes.io/name=pdb-webhook`

---

## Resource Usage

### CPU/Memory Limits (per pod)
- CPU Request: 100m / Limit: 500m
- Memory Request: 128Mi / Limit: 256Mi

### Replicas & Availability
- Replicas: 2 (for HA)
- Pod Anti-Affinity: Preferred (spreads across nodes)
- Update Strategy: RollingUpdate (default)

