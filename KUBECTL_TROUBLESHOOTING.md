# kubectl Troubleshooting Guide for XGrabba

This guide explains how to troubleshoot XGrabba running on the Kubernetes cluster (`nuc`) from your local Mac.

## Prerequisites

- kubectl configured to access the `nuc` cluster (see setup instructions)
- Access to the cluster via `kubectl get nodes` should work

## Quick Reference

### Basic Connection Test
```bash
# Verify cluster access
kubectl get nodes

# Check XGrabba namespace
kubectl get all -n xgrabba
```

## Common Troubleshooting Tasks

### 1. Check Pod Status

```bash
# List all pods in xgrabba namespace
kubectl get pods -n xgrabba

# Get detailed pod information
kubectl get pods -n xgrabba -o wide

# Describe a specific pod (shows events, volumes, etc.)
kubectl describe pod -n xgrabba <pod-name>

# Check pod resource usage
kubectl top pod -n xgrabba
```

### 2. View Logs

```bash
# View logs for main xgrabba pod
kubectl logs -n xgrabba -l app.kubernetes.io/name=xgrabba

# Follow logs in real-time
kubectl logs -n xgrabba -l app.kubernetes.io/name=xgrabba -f

# View logs from last hour
kubectl logs -n xgrabba -l app.kubernetes.io/name=xgrabba --since=1h

# View logs from specific pod
kubectl logs -n xgrabba <pod-name>

# View logs from all containers in a pod
kubectl logs -n xgrabba <pod-name> --all-containers=true

# View last N lines
kubectl logs -n xgrabba -l app.kubernetes.io/name=xgrabba --tail=100

# Search logs for specific terms
kubectl logs -n xgrabba -l app.kubernetes.io/name=xgrabba | grep -i "error"
kubectl logs -n xgrabba -l app.kubernetes.io/name=xgrabba | grep -i "encrypt"
```

### 3. Check Export/Encryption Activity

```bash
# Search for export-related logs
kubectl logs -n xgrabba -l app.kubernetes.io/name=xgrabba --since=24h | grep -E "(export|Export|encrypt|Encrypt)"

# Check for encryption progress
kubectl logs -n xgrabba -l app.kubernetes.io/name=xgrabba | grep -E "(encrypting|Encrypting|worker|parallel)"

# Monitor export status in real-time
kubectl logs -n xgrabba -l app.kubernetes.io/name=xgrabba -f | grep -E "(export|encrypt)"
```

### 4. Inspect Persistent Volumes

```bash
# List persistent volume claims
kubectl get pvc -n xgrabba

# Describe PVC (shows capacity, access modes, etc.)
kubectl describe pvc xgrabba -n xgrabba

# Check disk usage inside pod
kubectl exec -n xgrabba <pod-name> -- df -h /data

# List files in data directory
kubectl exec -n xgrabba <pod-name> -- ls -lah /data

# Check videos directory
kubectl exec -n xgrabba <pod-name> -- ls -lah /data/videos

# Check export directory (if exists)
kubectl exec -n xgrabba <pod-name> -- find /data -name "*export*" -o -name "*encrypted*" 2>/dev/null
```

### 5. Execute Commands in Pod

```bash
# Get shell access to pod
kubectl exec -it -n xgrabba <pod-name> -- /bin/sh

# Run a specific command
kubectl exec -n xgrabba <pod-name> -- ls -la /data

# Check environment variables
kubectl exec -n xgrabba <pod-name> -- env | grep -i "export\|encrypt"

# Check process list
kubectl exec -n xgrabba <pod-name> -- ps aux
```

### 6. Check Events and Status

```bash
# View recent events in namespace
kubectl get events -n xgrabba --sort-by='.lastTimestamp'

# View all events (more detailed)
kubectl get events -n xgrabba -o wide

# Check deployment status
kubectl get deployment -n xgrabba

# Describe deployment (shows rollout history, conditions)
kubectl describe deployment xgrabba -n xgrabba

# Check service endpoints
kubectl get endpoints -n xgrabba
```

### 7. Monitor Resource Usage

```bash
# Check pod CPU/memory usage
kubectl top pod -n xgrabba

# Check node resource usage
kubectl top node

# Check resource limits/requests
kubectl describe pod -n xgrabba <pod-name> | grep -A 5 "Limits\|Requests"
```

### 8. Debug Export/Encryption Issues

When troubleshooting export encryption (see GitHub issue #4):

```bash
# 1. Check if export is in progress
kubectl logs -n xgrabba -l app.kubernetes.io/name=xgrabba | grep -E "(export|encrypt)" | tail -50

# 2. Check for encryption worker activity
kubectl logs -n xgrabba -l app.kubernetes.io/name=xgrabba | grep -E "(worker|parallel|encrypting)" | tail -30

# 3. Monitor CPU usage during encryption
kubectl top pod -n xgrabba -w

# 4. Check export phase status
kubectl logs -n xgrabba -l app.kubernetes.io/name=xgrabba | grep -E "(phase|Phase)" | tail -20

# 5. Look for encryption errors
kubectl logs -n xgrabba -l app.kubernetes.io/name=xgrabba | grep -i "error.*encrypt"

# 6. Check export directory size
kubectl exec -n xgrabba <pod-name> -- du -sh /data/videos/* 2>/dev/null | sort -h
```

### 9. Restart/Scale Pods

```bash
# Restart a pod (delete it, deployment will recreate)
kubectl delete pod -n xgrabba <pod-name>

# Scale deployment
kubectl scale deployment xgrabba -n xgrabba --replicas=2

# Rollout restart (graceful restart)
kubectl rollout restart deployment xgrabba -n xgrabba

# Check rollout status
kubectl rollout status deployment xgrabba -n xgrabba
```

### 10. Check Configuration

```bash
# View ConfigMap
kubectl get configmap -n xgrabba
kubectl describe configmap xgrabba -n xgrabba

# View Secrets (values are base64 encoded)
kubectl get secrets -n xgrabba
kubectl describe secret xgrabba -n xgrabba

# Decode a secret value
kubectl get secret xgrabba -n xgrabba -o jsonpath='{.data.<key>}' | base64 -d
```

### 11. Network Debugging

```bash
# Port forward to access service locally
kubectl port-forward -n xgrabba service/xgrabba 9847:9847

# Then access http://localhost:9847 in browser

# Check service endpoints
kubectl get svc -n xgrabba
kubectl describe svc xgrabba -n xgrabba

# Test connectivity from pod
kubectl exec -n xgrabba <pod-name> -- wget -O- http://localhost:9847/health
```

### 12. Useful One-Liners

```bash
# Get pod name quickly
kubectl get pods -n xgrabba -l app.kubernetes.io/name=xgrabba -o jsonpath='{.items[0].metadata.name}'

# Watch pod status
kubectl get pods -n xgrabba -w

# Get logs from all pods matching label
kubectl logs -n xgrabba -l app.kubernetes.io/name=xgrabba --all-containers=true

# Count errors in logs
kubectl logs -n xgrabba -l app.kubernetes.io/name=xgrabba --since=1h | grep -i error | wc -l

# Find largest files in data directory
kubectl exec -n xgrabba <pod-name> -- find /data -type f -exec ls -lh {} \; | sort -k5 -h | tail -10
```

## Troubleshooting Specific Issues

### Issue: Pod not starting

```bash
# Check pod events
kubectl describe pod -n xgrabba <pod-name>

# Check for image pull errors
kubectl get events -n xgrabba | grep -i "pull\|image"

# Check resource constraints
kubectl top node
kubectl describe node nuc
```

### Issue: Export/Encryption seems stuck

```bash
# 1. Check export phase in logs
kubectl logs -n xgrabba -l app.kubernetes.io/name=xgrabba | grep -E "(phase|Phase|encrypting)"

# 2. Check if encryption workers are active
kubectl logs -n xgrabba -l app.kubernetes.io/name=xgrabba | grep -E "(worker|encrypting|parallel)"

# 3. Monitor CPU usage (should be high if parallel encryption working)
kubectl top pod -n xgrabba

# 4. Check disk I/O (if possible)
kubectl exec -n xgrabba <pod-name> -- iostat -x 1 5 2>/dev/null || echo "iostat not available"

# 5. Check for errors
kubectl logs -n xgrabba -l app.kubernetes.io/name=xgrabba | grep -i "error" | tail -20
```

### Issue: High memory usage

```bash
# Check current memory usage
kubectl top pod -n xgrabba

# Check memory limits
kubectl describe pod -n xgrabba <pod-name> | grep -A 3 "Limits"

# Check for memory-related errors
kubectl logs -n xgrabba -l app.kubernetes.io/name=xgrabba | grep -i "oom\|memory"
```

### Issue: Disk space issues

```bash
# Check PVC usage
kubectl describe pvc xgrabba -n xgrabba

# Check disk usage in pod
kubectl exec -n xgrabba <pod-name> -- df -h /data

# Find large directories
kubectl exec -n xgrabba <pod-name> -- du -sh /data/* 2>/dev/null | sort -h
```

## Getting Help

When reporting issues, include:

1. **Pod status**: `kubectl get pods -n xgrabba -o wide`
2. **Recent logs**: `kubectl logs -n xgrabba -l app.kubernetes.io/name=xgrabba --tail=100`
3. **Events**: `kubectl get events -n xgrabba --sort-by='.lastTimestamp'`
4. **Resource usage**: `kubectl top pod -n xgrabba`
5. **Pod description**: `kubectl describe pod -n xgrabba <pod-name>`

## Related Documentation

- Kubernetes CLI reference: https://kubernetes.io/docs/reference/kubectl/
- XGrabba GitHub issues: https://github.com/iconidentify/xgrabba/issues
- Encryption UX issue: https://github.com/iconidentify/xgrabba/issues/4
