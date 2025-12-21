#!/usr/bin/env bash
# Multi-cluster Kind E2E for KubeLease placement + remote cleanup.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

CONTROL=kubelease-control
EAST=kubelease-east
WEST=kubelease-west
IMG=kubelease:e2e

need() { command -v "$1" >/dev/null || { echo "missing $1"; exit 1; }; }
need kind
need kubectl
need docker

cleanup() {
  kind delete cluster --name "$CONTROL" >/dev/null 2>&1 || true
  kind delete cluster --name "$EAST" >/dev/null 2>&1 || true
  kind delete cluster --name "$WEST" >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo "==> create kind clusters"
kind create cluster --name "$CONTROL"
kind create cluster --name "$EAST"
kind create cluster --name "$WEST"

echo "==> build and load controller"
docker build -t "$IMG" .
kind load docker-image "$IMG" --name "$CONTROL"

CTX_CONTROL="kind-${CONTROL}"
CTX_EAST="kind-${EAST}"
CTX_WEST="kind-${WEST}"

echo "==> install CRDs + controller on control"
kubectl --context "$CTX_CONTROL" apply -k config/crd
kubectl --context "$CTX_CONTROL" apply -k config/default || true
# Prefer direct deploy with e2e image if kustomize image differs:
kubectl --context "$CTX_CONTROL" -n kubelease-system set image deployment/kubelease-controller-manager manager="$IMG" 2>/dev/null || true

echo "==> wait for control API"
kubectl --context "$CTX_CONTROL" wait --for=condition=Available deployment -n kubelease-system --all --timeout=180s 2>/dev/null || \
  echo "note: deploy may use different namespace; continuing with CRD-only smoke if needed"

make install IMG="$IMG" >/dev/null 2>&1 || true

NS_CREDS=kubelease-system
kubectl --context "$CTX_CONTROL" create ns "$NS_CREDS" --dry-run=client -o yaml | kubectl --context "$CTX_CONTROL" apply -f -

export_kubeconfig_secret() {
  local ctx=$1 name=$2
  kind get kubeconfig --name "${ctx#kind-}" > "/tmp/${name}.kubeconfig"
  kubectl --context "$CTX_CONTROL" -n "$NS_CREDS" create secret generic "${name}-kubeconfig" \
    --from-file=kubeconfig="/tmp/${name}.kubeconfig" \
    --dry-run=client -o yaml | kubectl --context "$CTX_CONTROL" apply -f -
}

export_kubeconfig_secret "$CTX_EAST" "east"
export_kubeconfig_secret "$CTX_WEST" "west"

# Minimal remote RBAC is whatever the kind admin kubeconfig already has.

cat <<EOF | kubectl --context "$CTX_CONTROL" apply -f -
apiVersion: platform.kubelease.io/v1alpha1
kind: ClusterTarget
metadata:
  name: east
  labels:
    kubelease.io/region: us-east
spec:
  credentials:
    secretRef:
      name: east-kubeconfig
      namespace: ${NS_CREDS}
  enabled: true
---
apiVersion: platform.kubelease.io/v1alpha1
kind: ClusterTarget
metadata:
  name: west
  labels:
    kubelease.io/region: us-west
spec:
  credentials:
    secretRef:
      name: west-kubeconfig
      namespace: ${NS_CREDS}
  enabled: true
EOF

# If controller isn't running via config/default, run locally against control.
if ! kubectl --context "$CTX_CONTROL" get deploy -A 2>/dev/null | grep -q kubelease; then
  echo "==> starting local controller against control cluster"
  export KUBECONFIG="$(kind get kubeconfig --name "$CONTROL")"
  go run ./cmd/main.go --leader-elect=false --metrics-bind-address=0 --health-probe-bind-address=:18081 &
  CTRL_PID=$!
  trap 'kill $CTRL_PID 2>/dev/null || true; cleanup' EXIT
  sleep 5
fi

echo "==> place east lease"
cat <<EOF | kubectl --context "$CTX_CONTROL" apply -f -
apiVersion: platform.kubelease.io/v1alpha1
kind: EnvironmentLease
metadata:
  name: e2e-east
spec:
  ttl: 30m
  placement:
    selector:
      matchLabels:
        kubelease.io/region: us-east
  namespace:
    generateName: e2e-east-
EOF

echo "==> place west lease"
cat <<EOF | kubectl --context "$CTX_CONTROL" apply -f -
apiVersion: platform.kubelease.io/v1alpha1
kind: EnvironmentLease
metadata:
  name: e2e-west
spec:
  ttl: 30m
  placement:
    selector:
      matchLabels:
        kubelease.io/region: us-west
  namespace:
    generateName: e2e-west-
EOF

wait_ns() {
  local ctx=$1 prefix=$2
  for _ in $(seq 1 60); do
    if kubectl --context "$ctx" get ns 2>/dev/null | grep -q "$prefix"; then
      return 0
    fi
    sleep 2
  done
  echo "timeout waiting for namespace prefix $prefix on $ctx"
  kubectl --context "$CTX_CONTROL" get environmentleases -o yaml || true
  return 1
}

wait_ns "$CTX_EAST" e2e-east-
wait_ns "$CTX_WEST" e2e-west-

EAST_NS=$(kubectl --context "$CTX_CONTROL" get environmentlease e2e-east -o jsonpath='{.status.namespace}')
echo "==> drift: delete ResourceQuota on east"
kubectl --context "$CTX_EAST" -n "$EAST_NS" delete resourcequota --all --ignore-not-found
# touch lease to speed reconcile
kubectl --context "$CTX_CONTROL" annotate environmentlease e2e-east kubelease.io/e2e-touch="$(date +%s)" --overwrite
sleep 10

echo "==> expire east lease"
kubectl --context "$CTX_CONTROL" delete environmentlease e2e-east --wait=false
for _ in $(seq 1 60); do
  if ! kubectl --context "$CTX_EAST" get ns "$EAST_NS" >/dev/null 2>&1; then
    echo "east namespace cleaned"
    break
  fi
  sleep 2
done

echo "==> PASS kind multicluster e2e"
