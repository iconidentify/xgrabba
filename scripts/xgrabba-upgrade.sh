#!/bin/bash
# Trigger xgrabba upgrade to latest Helm chart version
# Supports both main deployment and USB Manager DaemonSet

set -euo pipefail

NAMESPACE="${NAMESPACE:-xgrabba}"
RELEASE_NAME="${RELEASE_NAME:-xgrabba}"

echo "=== XGrabba Upgrade ==="
echo

if ! command -v kubectl >/dev/null 2>&1; then
  echo "ERROR: kubectl not found in PATH"
  exit 1
fi

if ! command -v helm >/dev/null 2>&1; then
  echo "ERROR: helm not found in PATH"
  exit 1
fi

# Check current versions
echo "Current versions:"
CHART_VERSION=$(kubectl get release -n "$NAMESPACE" "$RELEASE_NAME" -o jsonpath='{.spec.forProvider.chart.version}' 2>/dev/null || echo "unknown")
echo "  Chart: $CHART_VERSION"

# Get main deployment image
MAIN_IMAGE=$(kubectl get pod -n "$NAMESPACE" -l app.kubernetes.io/name=xgrabba -o jsonpath='{.items[0].spec.containers[0].image}' 2>/dev/null || echo "not found")
echo "  Main image: $MAIN_IMAGE"

# Check if USB Manager is deployed (DaemonSet)
USB_MANAGER_ENABLED=false
USB_DS_NAME="${RELEASE_NAME}-usb-manager"
USB_MANAGER_IMAGE=$(kubectl get daemonset -n "$NAMESPACE" "$USB_DS_NAME" -o jsonpath='{.spec.template.spec.containers[0].image}' 2>/dev/null || true)
if [ -n "$USB_MANAGER_IMAGE" ]; then
  USB_MANAGER_ENABLED=true
  echo "  USB Manager: $USB_MANAGER_IMAGE"
  USB_NODES=$(kubectl get daemonset -n "$NAMESPACE" "$USB_DS_NAME" -o jsonpath='{.status.numberReady}/{.status.desiredNumberScheduled}' 2>/dev/null)
  echo "  USB Manager nodes: $USB_NODES"
fi
echo

# Check latest available
echo "Checking latest available..."
LATEST=$(helm show chart oci://ghcr.io/iconidentify/charts/xgrabba 2>/dev/null | grep '^version:' | awk '{print $2}')
if [ -z "$LATEST" ]; then
  echo "ERROR: Could not fetch latest chart version"
  exit 1
fi
echo "  Latest chart: $LATEST"

# Compare versions
if [ "$CHART_VERSION" = "$LATEST" ]; then
  echo
  echo "Already at latest version ($LATEST)"
  echo "Use --force to upgrade anyway"
  if [ "${1:-}" != "--force" ]; then
    exit 0
  fi
  echo "Forcing upgrade..."
fi

echo
echo "Triggering upgrade..."
# Remove pinned chart version to use latest
kubectl patch release -n "$NAMESPACE" "$RELEASE_NAME" --type=json -p='[{"op": "remove", "path": "/spec/forProvider/chart/version"}]' 2>/dev/null || true
# Remove hardcoded image tag so chart's appVersion is used
kubectl patch release -n "$NAMESPACE" "$RELEASE_NAME" --type=json -p='[{"op": "remove", "path": "/spec/forProvider/values/image/tag"}]' 2>/dev/null || true

echo "Waiting for Crossplane to reconcile..."
sleep 5

echo "Monitoring rollout..."
echo

# Wait for Helm release to be ready
for i in {1..60}; do
  VERSION=$(kubectl get release -n "$NAMESPACE" "$RELEASE_NAME" -o jsonpath='{.spec.forProvider.chart.version}' 2>/dev/null || echo "")
  READY=$(kubectl get release -n "$NAMESPACE" "$RELEASE_NAME" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || echo "")
  SYNCED=$(kubectl get release -n "$NAMESPACE" "$RELEASE_NAME" -o jsonpath='{.status.conditions[?(@.type=="Synced")].status}' 2>/dev/null || echo "")

  printf "\r  Release: version=%s ready=%s synced=%s " "$VERSION" "$READY" "$SYNCED"

  if [ "$VERSION" = "$LATEST" ] && [ "$READY" = "True" ]; then
    echo
    break
  fi

  if [ $i -eq 60 ]; then
    echo
    echo "WARNING: Timeout waiting for Helm release - continuing to check pods..."
  fi
  sleep 2
done

echo
echo "Checking deployment rollout..."

# Wait for main deployment
echo "  Main deployment:"
kubectl rollout status deployment/"$RELEASE_NAME" -n "$NAMESPACE" --timeout=120s 2>/dev/null || {
  echo "  WARNING: Main deployment rollout not complete"
}

# Wait for USB Manager DaemonSet if enabled
if [ "$USB_MANAGER_ENABLED" = "true" ]; then
  echo "  USB Manager DaemonSet:"
  kubectl rollout status daemonset/"$USB_DS_NAME" -n "$NAMESPACE" --timeout=120s 2>/dev/null || {
    echo "  WARNING: USB Manager rollout not complete"
  }
fi

echo
echo "=== Upgrade Complete ==="
echo

# Show final status
echo "Final status:"
echo

echo "Pods:"
kubectl get pods -n "$NAMESPACE" -o wide

echo
echo "Images:"
kubectl get pods -n "$NAMESPACE" -o jsonpath='{range .items[*]}{.metadata.name}: {.spec.containers[*].image}{"\n"}{end}'

# Check for any issues
echo
RESTART_COUNT=$(kubectl get pods -n "$NAMESPACE" -o jsonpath='{.items[*].status.containerStatuses[*].restartCount}' | tr ' ' '\n' | awk '{s+=$1} END {print s}')
if [ "${RESTART_COUNT:-0}" -gt "0" ]; then
  echo "WARNING: Total container restarts: $RESTART_COUNT"
  echo "Check logs if pods are crash-looping:"
  echo "  kubectl logs -n $NAMESPACE <pod-name>"
fi

# Health check with retry (pods may need a moment to initialize)
echo
echo "Health checks:"

# Function to check health with retries
check_health() {
  local pod=$1
  local port=$2
  local name=$3
  local max_attempts=10
  local attempt=1

  while [ $attempt -le $max_attempts ]; do
    HEALTH=$(kubectl exec -n "$NAMESPACE" "$pod" -- sh -c "wget -q -O- http://localhost:$port/health 2>/dev/null || curl -sf http://localhost:$port/health 2>/dev/null || true")
    if [ -n "$HEALTH" ] && [ "$HEALTH" != "failed" ]; then
      echo "  $name: $HEALTH"
      return 0
    fi
    if [ $attempt -lt $max_attempts ]; then
      printf "  $name: waiting for health check... (attempt %d/%d)\r" $attempt $max_attempts
      sleep 2
    fi
    attempt=$((attempt + 1))
  done
  echo "  $name: health check failed after $max_attempts attempts"
  return 1
}

MAIN_POD=$(kubectl get pod -n "$NAMESPACE" -l app.kubernetes.io/name=xgrabba -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
if [ -n "$MAIN_POD" ]; then
  check_health "$MAIN_POD" 9847 "Main app"
fi

if [ "$USB_MANAGER_ENABLED" = "true" ]; then
  USB_POD=$(kubectl get pod -n "$NAMESPACE" -l app.kubernetes.io/name=xgrabba-usb-manager -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
  if [ -n "$USB_POD" ]; then
    check_health "$USB_POD" 8080 "USB Manager"
  fi
fi

echo
echo "Done!"
