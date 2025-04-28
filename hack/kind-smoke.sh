#!/usr/bin/env bash
# Fast Kind smoke: install CRDs + controller, create a lease, wait for Active, expire.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

IMG="${IMG:-kubelease:smoke}"
CLUSTER="${KIND_CLUSTER:-kubelease-smoke}"

echo "==> Building image ${IMG}"
docker build -t "${IMG}" .

echo "==> Loading into kind (${CLUSTER})"
kind load docker-image "${IMG}" --name "${CLUSTER}"

echo "==> Installing CRDs"
make install

echo "==> Deploying controller"
cd config/manager && "$(cd ../.. && pwd)/bin/kustomize" edit set image controller="${IMG}"
cd "$ROOT"
# Ensure kustomize binary exists
make kustomize >/dev/null
./bin/kustomize build config/default | kubectl apply -f -

echo "==> Waiting for controller"
kubectl -n kubelease-system rollout status deploy/kubelease-controller-manager --timeout=180s

echo "==> Applying sample policy"
kubectl apply -f config/samples/platform_v1alpha1_environmentleasepolicy.yaml

echo "==> Creating smoke lease"
cat <<'EOF' | kubectl apply -f -
apiVersion: platform.kubelease.io/v1alpha1
kind: EnvironmentLease
metadata:
  name: smoke-demo
spec:
  policyRef:
    name: preview-default
  ttl: 30m
  maxTTL: 2h
  namespace:
    name: preview-smoke-demo
EOF

echo "==> Waiting for Active"
kubectl wait --for=jsonpath='{.status.phase}'=Active envlease/smoke-demo --timeout=120s
NS="$(kubectl get envlease smoke-demo -o jsonpath='{.status.namespace}')"
test -n "${NS}"
kubectl get ns "${NS}" >/dev/null

echo "==> Expiring lease"
kubectl delete envlease smoke-demo --wait=false

echo "==> Waiting for Namespace cleanup"
for _ in $(seq 1 60); do
  if ! kubectl get ns "${NS}" >/dev/null 2>&1; then
    echo "Namespace ${NS} removed"
    echo "SMOKE OK"
    exit 0
  fi
  sleep 2
done
echo "timed out waiting for Namespace ${NS} deletion" >&2
exit 1
