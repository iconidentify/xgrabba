# Kubernetes config management (XGrabba)

This doc explains **where to set configuration variables** for XGrabba when running in Kubernetes, and how to verify changes.

XGrabba is deployed via a Helm chart. In this repo, the cluster is typically managed by **Crossplane provider-helm**, using `deployments/crossplane/release.yaml`.

## What to edit (source of truth)

- **Non-secret config (env vars / toggles)**: edit the Helm values in:
  - `deployments/crossplane/release.yaml` → `spec.forProvider.values.config.*`
  - or in your GitOps system’s equivalent HelmRelease/Release values.

- **Secrets**: stored in Kubernetes Secret `xgrabba-secrets` in namespace `xgrabba`
  - repo template: `deployments/crossplane/secrets.yaml`

## Bookmarks via browser-session GraphQL

To enable the new bookmarks mode that uses extension-forwarded browser credentials:

- Set:
  - `config.bookmarks.enabled: true`
  - `config.bookmarks.useBrowserCredentials: true`

Notes:
- In browser mode, you **do not** need `config.bookmarks.userId` or OAuth tokens.
- The monitor will only succeed once the extension has forwarded credentials (see below).

## How to change config in the cluster (kubectl)

### 1) Inspect current deployed values (Crossplane Release)

```bash
kubectl get release xgrabba -o yaml | sed -n '1,200p'
```

### 2) Patch the Release values (quick change)

This toggles bookmarks browser mode on:

```bash
kubectl patch release xgrabba --type merge -p '{
  "spec": {
    "forProvider": {
      "values": {
        "config": {
          "bookmarks": {
            "enabled": true,
            "useBrowserCredentials": true
          }
        }
      }
    }
  }
}'
```

### 3) Watch the rollout

```bash
kubectl -n xgrabba rollout status deployment/xgrabba --timeout=5m
kubectl -n xgrabba get pods -owide
```

## Verifying the env vars on the running pod

```bash
POD="$(kubectl -n xgrabba get pod -l app.kubernetes.io/name=xgrabba -o jsonpath='{.items[0].metadata.name}')"
kubectl -n xgrabba exec "$POD" -- env | rg 'BOOKMARKS_|API_KEY|GROK_'
```

## Extension prerequisites (required for browser modes)

1. In the extension popup settings, set:
   - backend URL (your ingress / port-forward URL)
   - API key (must match `API_KEY` in the server)
   - enable **Forward X credentials**

2. Visit `x.com` while logged in (so the extension can capture `auth_token` + `ct0`).

3. Verify server received credentials:

```bash
curl -H "X-API-Key: $API_KEY" http://<xgrabba-host>/api/v1/extension/credentials/status
```

You want: `has_credentials: true` and `is_expired: false`.

## Common pitfalls

- **Bookmarks monitor enabled but “Disconnected”**:
  - Browser mode requires the extension to sync credentials at least once.
  - Confirm the extension is pointed at the correct backend URL and API key.

- **No release rollout after patching Crossplane Release**:
  - provider-helm reconciles on its own interval; check:
    - `kubectl describe release xgrabba`

