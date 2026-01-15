#!/bin/bash
# XGrabba status and troubleshooting script
# Shows detailed status of all components including USB Manager

set -euo pipefail

NAMESPACE="${NAMESPACE:-xgrabba}"
RELEASE_NAME="${RELEASE_NAME:-xgrabba}"

echo "=== XGrabba Status ==="
echo "Namespace: $NAMESPACE"
echo "Time: $(date)"
echo

if ! command -v kubectl >/dev/null 2>&1; then
  echo "ERROR: kubectl not found in PATH"
  exit 1
fi

# Helm Release Status (Crossplane Release CRD)
echo "--- Helm Release ---"
kubectl get release -n "$NAMESPACE" "$RELEASE_NAME" -o jsonpath='Chart: {.spec.forProvider.chart.name}:{.spec.forProvider.chart.version}
Ready: {.status.conditions[?(@.type=="Ready")].status}
Synced: {.status.conditions[?(@.type=="Synced")].status}
' 2>/dev/null || echo "Release not found (not using Crossplane?)"
echo

# Pods
echo "--- Pods ---"
kubectl get pods -n "$NAMESPACE" -o wide
echo

# Deployments
echo "--- Deployments ---"
kubectl get deployments -n "$NAMESPACE"
echo

# DaemonSets (USB Manager)
echo "--- DaemonSets ---"
DS_COUNT=$(kubectl get daemonsets -n "$NAMESPACE" --no-headers 2>/dev/null | wc -l | tr -d ' ')
if [ "${DS_COUNT:-0}" -gt 0 ]; then
  kubectl get daemonsets -n "$NAMESPACE"
else
  echo "(none - USB Manager not enabled)"
fi
echo

# Services
echo "--- Services ---"
kubectl get services -n "$NAMESPACE"
echo

# PVCs
echo "--- Persistent Volume Claims ---"
kubectl get pvc -n "$NAMESPACE"
echo

# Recent Events
echo "--- Recent Events (last 10) ---"
kubectl get events -n "$NAMESPACE" --sort-by='.lastTimestamp' | tail -11 | head -10
echo

# Container Status Details
echo "--- Container Details ---"
kubectl get pods -n "$NAMESPACE" -o jsonpath='{range .items[*]}
Pod: {.metadata.name}
  Status: {.status.phase}
  Ready: {.status.conditions[?(@.type=="Ready")].status}
  Restarts: {range .status.containerStatuses[*]}{.name}={.restartCount} {end}
  Image: {.spec.containers[*].image}
{end}'
echo

# Health Checks
echo "--- Health Checks ---"

health_check_cmd='wget -q -O- http://localhost:%s/%s 2>/dev/null || curl -sf http://localhost:%s/%s 2>/dev/null || true'

# Main app
MAIN_POD=$(kubectl get pod -n "$NAMESPACE" -l app.kubernetes.io/name=xgrabba -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
if [ -n "$MAIN_POD" ]; then
  echo "Main app ($MAIN_POD):"
  HEALTH=$(kubectl exec -n "$NAMESPACE" "$MAIN_POD" -- sh -c "$(printf "$health_check_cmd" 9847 health 9847 health)")
  if [ -n "$HEALTH" ]; then
    echo "  /health: $HEALTH"
  else
    echo "  /health: FAILED"
  fi
  READY=$(kubectl exec -n "$NAMESPACE" "$MAIN_POD" -- sh -c "$(printf "$health_check_cmd" 9847 ready 9847 ready)")
  if [ -n "$READY" ]; then
    echo "  /ready: $READY"
  else
    echo "  /ready: FAILED"
  fi
fi

# USB Manager
USB_PODS=$(kubectl get pods -n "$NAMESPACE" -l app.kubernetes.io/name=xgrabba-usb-manager -o jsonpath='{.items[*].metadata.name}' 2>/dev/null || true)
if [ -n "$USB_PODS" ]; then
  echo
  echo "USB Manager:"
  for USB_POD in $USB_PODS; do
    NODE=$(kubectl get pod -n "$NAMESPACE" "$USB_POD" -o jsonpath='{.spec.nodeName}')
    echo "  $USB_POD (node: $NODE):"
    USB_HEALTH=$(kubectl exec -n "$NAMESPACE" "$USB_POD" -- sh -c "$(printf "$health_check_cmd" 8080 health 8080 health)")
    if [ -n "$USB_HEALTH" ]; then
      echo "    /health: $USB_HEALTH"
    else
      echo "    /health: FAILED"
    fi
    # List detected USB drives
    DRIVES=$(kubectl exec -n "$NAMESPACE" "$USB_POD" -- sh -c "$(printf "$health_check_cmd" 8080 api/v1/drives 8080 api/v1/drives)")
    if [ -n "$DRIVES" ]; then
      echo "    Drives: $DRIVES"
    fi
  done
fi

echo

# Check for problems
echo "--- Potential Issues ---"
ISSUES=0

# Check for crash loops
CRASH_PODS=$(kubectl get pods -n "$NAMESPACE" -o jsonpath='{range .items[*]}{.metadata.name}:{.status.containerStatuses[*].restartCount}{"\n"}{end}' | grep -v ':0$' | grep -v ':$' || true)
if [ -n "$CRASH_PODS" ]; then
  echo "! Pods with restarts:"
  echo "$CRASH_PODS" | while read -r line; do echo "    $line"; done
  ISSUES=$((ISSUES + 1))
fi

# Check for pending pods
PENDING=$(kubectl get pods -n "$NAMESPACE" --field-selector=status.phase=Pending -o jsonpath='{.items[*].metadata.name}' || true)
if [ -n "$PENDING" ]; then
  echo "! Pending pods: $PENDING"
  ISSUES=$((ISSUES + 1))
fi

# Check for failed pods
FAILED=$(kubectl get pods -n "$NAMESPACE" --field-selector=status.phase=Failed -o jsonpath='{.items[*].metadata.name}' || true)
if [ -n "$FAILED" ]; then
  echo "! Failed pods: $FAILED"
  ISSUES=$((ISSUES + 1))
fi

# Check PVC binding
UNBOUND=$(kubectl get pvc -n "$NAMESPACE" -o jsonpath='{range .items[?(@.status.phase!="Bound")]}{.metadata.name}{"\n"}{end}' || true)
if [ -n "$UNBOUND" ]; then
  echo "! Unbound PVCs: $UNBOUND"
  ISSUES=$((ISSUES + 1))
fi

if [ $ISSUES -eq 0 ]; then
  echo "(none detected)"
fi

echo
echo "=== Quick Commands ==="
echo "View main logs:      kubectl logs -n $NAMESPACE -l app.kubernetes.io/name=xgrabba -f"
echo "View USB mgr logs:   kubectl logs -n $NAMESPACE -l app.kubernetes.io/name=xgrabba-usb-manager -f"
echo "Describe main pod:   kubectl describe pod -n $NAMESPACE -l app.kubernetes.io/name=xgrabba"
echo "Port forward UI:     kubectl port-forward -n $NAMESPACE svc/$RELEASE_NAME 9847:9847"
echo "Shell into main:     kubectl exec -it -n $NAMESPACE \$(kubectl get pod -n $NAMESPACE -l app.kubernetes.io/name=xgrabba -o name | head -1) -- sh"
echo
