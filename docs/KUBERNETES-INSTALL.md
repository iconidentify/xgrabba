# XGrabba Kubernetes Installation Guide

XGrabba is a tweet archival tool with a browser extension and Go backend. This guide covers deploying the backend to Kubernetes using Crossplane.

## Prerequisites

1. **Kubernetes cluster** (1.24+)
2. **Crossplane** installed in the cluster
3. **provider-helm** installed and configured

### Install Crossplane (if not already installed)

```bash
helm repo add crossplane-stable https://charts.crossplane.io/stable
helm repo update
helm install crossplane crossplane-stable/crossplane \
  --namespace crossplane-system \
  --create-namespace
```

### Install provider-helm

```bash
kubectl apply -f - <<EOF
apiVersion: pkg.crossplane.io/v1
kind: Provider
metadata:
  name: provider-helm
spec:
  package: xpkg.upbound.io/crossplane-contrib/provider-helm:v0.19.0
EOF

# Wait for provider to be ready
kubectl wait --for=condition=healthy provider/provider-helm --timeout=300s
```

### Configure provider-helm

```bash
kubectl apply -f - <<EOF
apiVersion: helm.crossplane.io/v1beta1
kind: ProviderConfig
metadata:
  name: helm-provider
spec:
  credentials:
    source: InjectedIdentity
EOF
```

Grant provider-helm cluster-admin (required for namespace creation):

```bash
SA=$(kubectl -n crossplane-system get sa -o name | grep provider-helm | head -1 | sed 's/serviceaccount\///')
kubectl create clusterrolebinding provider-helm-admin \
  --clusterrole cluster-admin \
  --serviceaccount crossplane-system:$SA
```

---

## Installation

### Step 1: Create GHCR Credentials for Helm Charts

The Helm chart is stored in GitHub Container Registry (GHCR) which requires authentication.

```bash
# Create credentials for Crossplane to pull Helm charts from GHCR
# Replace USERNAME with your GitHub username and GITHUB_TOKEN with a PAT with read:packages scope
kubectl create secret generic ghcr-helm-credentials \
  --namespace crossplane-system \
  --from-file=credentials=<(echo '{"auths":{"ghcr.io":{"auth":"'$(echo -n "USERNAME:GITHUB_TOKEN" | base64)'"}}}')
```

### Step 2: Create Namespace and Secrets

```bash
# Create namespace
kubectl create namespace xgrabba

# Create image pull secret for Docker images
kubectl create secret docker-registry ghcr-secret \
  --namespace xgrabba \
  --docker-server=ghcr.io \
  --docker-username=USERNAME \
  --docker-password=GITHUB_TOKEN

# Generate a secure API key
API_KEY=$(openssl rand -hex 32)
echo "Generated API Key: $API_KEY"
echo "Save this key - you'll need it for the browser extension!"

# Create app secrets
kubectl create secret generic xgrabba-secrets \
  --namespace xgrabba \
  --from-literal=api-key="$API_KEY" \
  --from-literal=grok-api-key="YOUR_GROK_API_KEY"
```

Replace `YOUR_GROK_API_KEY` with your Grok API key from [x.ai](https://x.ai).

### Step 3: Deploy XGrabba

```bash
kubectl apply -f https://raw.githubusercontent.com/iconidentify/xgrabba/main/deployments/crossplane/install.yaml
```

### Step 4: Verify Deployment

```bash
# Check Crossplane release status
kubectl get release xgrabba

# Check pods
kubectl get pods -n xgrabba

# Check service
kubectl get svc -n xgrabba
```

### Step 5: Access the Service

**Option A: Port Forward (for testing)**
```bash
kubectl port-forward -n xgrabba svc/xgrabba 9847:9847
# Access at http://localhost:9847
```

**Option B: Create an Ingress**
```bash
kubectl apply -f - <<EOF
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: xgrabba
  namespace: xgrabba
  annotations:
    # Add your ingress annotations (cert-manager, etc.)
spec:
  ingressClassName: nginx  # or your ingress class
  rules:
    - host: xgrabba.yourdomain.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: xgrabba
                port:
                  number: 9847
EOF
```

**Option C: LoadBalancer Service**
```bash
kubectl patch svc xgrabba -n xgrabba -p '{"spec": {"type": "LoadBalancer"}}'
kubectl get svc xgrabba -n xgrabba -w  # Wait for external IP
```

---

## Optional: Enable Samba File Sharing

Samba allows browsing archived tweets from Mac Finder or Windows Explorer.

### Step 1: Add Samba Password to Secrets

```bash
# Delete and recreate the secret with samba password included
kubectl delete secret xgrabba-secrets -n xgrabba

kubectl create secret generic xgrabba-secrets \
  --namespace xgrabba \
  --from-literal=api-key="YOUR_API_KEY" \
  --from-literal=grok-api-key="YOUR_GROK_API_KEY" \
  --from-literal=samba-password="YOUR_SAMBA_PASSWORD"
```

Replace the API keys with your actual values.

### Step 2: Deploy with Samba Enabled

Download and modify the install manifest:

```bash
curl -o xgrabba-install.yaml \
  https://raw.githubusercontent.com/iconidentify/xgrabba/main/deployments/crossplane/install.yaml
```

Edit `xgrabba-install.yaml` and uncomment the Samba sections:

```yaml
# In the values section, uncomment:
samba:
  enabled: true
  username: xgrabba
  shareName: archives
  service:
    type: LoadBalancer

# In the set section, uncomment:
- name: samba.password
  valueFrom:
    secretKeyRef:
      name: xgrabba-secrets
      key: samba-password
      namespace: xgrabba
```

Apply the modified manifest:

```bash
kubectl apply -f xgrabba-install.yaml
```

### Step 3: Connect to Samba Share

Get the Samba service IP:

```bash
kubectl get svc xgrabba-samba -n xgrabba
```

**Mac:**
1. Open Finder
2. Press `Cmd+K` (Go > Connect to Server)
3. Enter: `smb://SAMBA_IP/archives`
4. Login: username `xgrabba`, password from secret

**Windows:**
1. Open File Explorer
2. Enter in address bar: `\\SAMBA_IP\archives`
3. Login: username `xgrabba`, password from secret

---

## Configuration Reference

### Helm Values

| Parameter | Description | Default |
|-----------|-------------|---------|
| `replicaCount` | Number of replicas | `1` |
| `config.worker.count` | Background workers | `2` |
| `config.grok.model` | Grok AI model | `grok-3` |
| `persistence.enabled` | Enable persistent storage | `true` |
| `persistence.size` | Storage size | `100Gi` |
| `samba.enabled` | Enable Samba sidecar | `false` |

### Customizing the Deployment

Download and edit the manifest:

```bash
curl -o xgrabba-install.yaml \
  https://raw.githubusercontent.com/iconidentify/xgrabba/main/deployments/crossplane/install.yaml
```

Example customizations:

```yaml
values:
  # Increase storage
  persistence:
    size: 500Gi
    storageClass: "fast-ssd"

  # More workers for faster processing
  config:
    worker:
      count: 4

  # More resources for heavy video processing
  resources:
    requests:
      cpu: 500m
      memory: 1Gi
    limits:
      cpu: 2000m
      memory: 8Gi
```

---

## Web UI Access

Once deployed, the following endpoints are available:

| Endpoint | Description |
|----------|-------------|
| `/` | Smart UI (auto-detects mobile/desktop) |
| `/ui` | Full archive browser (desktop) |
| `/quick` or `/q` | Quick archive (mobile-optimized) |
| `/health` | Liveness probe |
| `/ready` | Readiness probe |

### Mobile Quick Archive

For mobile users, bookmark: `https://xgrabba.yourdomain.com/q?key=YOUR_API_KEY`

This provides a streamlined interface for quickly pasting and archiving tweet URLs.

---

## Browser Extension Setup

1. Install the XGrabba extension (Chrome/Edge)
2. Click the extension icon > Settings
3. Set Backend URL: `https://xgrabba.yourdomain.com`
4. Set API Key: (the key generated during installation)
5. Save Settings

---

## Auto-Updates

By default, Crossplane automatically updates XGrabba when new chart versions are published. The reconciliation interval is approximately 1 hour.

### How It Works

1. When you push a new version tag (e.g., `v0.2.0`) to the xgrabba repository
2. GitHub Actions builds and publishes:
   - Docker image: `ghcr.io/iconidentify/xgrabba:v0.2.0`
   - Helm chart: `oci://ghcr.io/iconidentify/charts/xgrabba:0.2.0`
3. Crossplane detects the new chart version during its next reconciliation
4. The Release is automatically upgraded

### Force Immediate Update

To trigger an immediate reconciliation:

```bash
kubectl annotate release xgrabba crossplane.io/refresh=$(date +%s) --overwrite
```

### Pin to Specific Version

To disable auto-updates and pin to a specific version, download the install.yaml and add a version field:

```yaml
spec:
  forProvider:
    chart:
      name: xgrabba
      repository: oci://ghcr.io/iconidentify/charts
      version: "0.2.0"  # Add this line to pin
```

### Check Current Version

```bash
# Check deployed chart version
kubectl get release xgrabba -o jsonpath='{.spec.forProvider.chart.version}'

# Check running image version
kubectl get pod -n xgrabba -o jsonpath='{.items[0].spec.containers[0].image}'
```

---

## Troubleshooting

### Check Logs

```bash
kubectl logs -n xgrabba -l app.kubernetes.io/name=xgrabba -f
```

### Check Crossplane Release Status

```bash
kubectl describe release xgrabba
```

### Common Issues

**Release stuck in "not ready" state:**
- Check GHCR credentials: `kubectl get secret ghcr-helm-credentials -n crossplane-system`
- Describe release for errors: `kubectl describe release xgrabba`
- Check provider-helm logs: `kubectl logs -n crossplane-system -l pkg.crossplane.io/provider=provider-helm`

**Pod not starting / ImagePullBackOff:**
- Check image pull secret exists: `kubectl get secret ghcr-secret -n xgrabba`
- Verify the secret has correct credentials: `kubectl get secret ghcr-secret -n xgrabba -o jsonpath='{.data.\.dockerconfigjson}' | base64 -d`
- Check pod events: `kubectl describe pod -n xgrabba -l app.kubernetes.io/name=xgrabba`

**Pod CrashLoopBackOff:**
- Check app secrets exist: `kubectl get secret xgrabba-secrets -n xgrabba`
- Check PVC is bound: `kubectl get pvc -n xgrabba`
- Check pod logs: `kubectl logs -n xgrabba -l app.kubernetes.io/name=xgrabba`

**API returns 401:**
- Verify API key matches between extension and secret
- Retrieve API key: `kubectl get secret xgrabba-secrets -n xgrabba -o jsonpath='{.data.api-key}' | base64 -d`

**Samba not accessible:**
- Check samba service has external IP: `kubectl get svc xgrabba-samba -n xgrabba`
- Verify firewall allows port 445

---

## Uninstall

```bash
kubectl delete -f https://raw.githubusercontent.com/iconidentify/xgrabba/main/deployments/crossplane/install.yaml
kubectl delete namespace xgrabba
```
