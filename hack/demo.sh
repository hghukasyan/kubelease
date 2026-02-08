#!/usr/bin/env bash
# One-command local demo: Kind cluster + CRDs + controller + sample EnvironmentLease.
# Idempotent where practical. Does not delete an existing cluster unless DEMO_RECREATE=1.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

CLUSTER="${KIND_CLUSTER:-kubelease-demo}"
IMG="${IMG:-kubelease:demo}"
LEASE_NAME="${LEASE_NAME:-demo}"

need() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "error: required command not found: $1" >&2
    exit 1
  }
}

need kind
need docker
need kubectl
need go

echo "==> KubeLease demo"
echo "    Kind cluster: ${CLUSTER}"
echo "    Image:        ${IMG}"
echo

if kind get clusters 2>/dev/null | grep -qx "${CLUSTER}"; then
  if [[ "${DEMO_RECREATE:-}" == "1" ]]; then
    echo "==> Recreating Kind cluster ${CLUSTER}"
    kind delete cluster --name "${CLUSTER}"
    kind create cluster --name "${CLUSTER}"
  else
    echo "==> Reusing existing Kind cluster ${CLUSTER}"
    echo "    (set DEMO_RECREATE=1 to delete and recreate)"
  fi
else
  echo "==> Creating Kind cluster ${CLUSTER}"
  kind create cluster --name "${CLUSTER}"
fi

kubectl config use-context "kind-${CLUSTER}" >/dev/null

echo "==> Building controller image"
docker build -t "${IMG}" .

echo "==> Loading image into Kind"
kind load docker-image "${IMG}" --name "${CLUSTER}"

echo "==> Installing CRDs"
make install

echo "==> Ensuring kustomize + setting image"
make kustomize >/dev/null
(
  cd config/manager
  "${ROOT}/bin/kustomize" edit set image "controller=${IMG}"
)

echo "==> Deploying controller"
"${ROOT}/bin/kustomize" build config/default | kubectl apply -f -

echo "==> Waiting for controller rollout"
kubectl -n kubelease-system rollout status deploy/kubelease-controller-manager --timeout=180s

echo "==> Applying sample policy"
kubectl apply -f config/samples/platform_v1alpha1_environmentleasepolicy.yaml

echo "==> Building CLI"
make cli
export PATH="${ROOT}/bin:${PATH}"

if kubectl get envlease "${LEASE_NAME}" >/dev/null 2>&1; then
  echo "==> Lease ${LEASE_NAME} already exists — skipping create"
else
  echo "==> Creating EnvironmentLease/${LEASE_NAME}"
  kubectl kubelease create "${LEASE_NAME}" \
    --ttl 30m \
    --max-ttl 2h \
    --policy preview-default \
    --owner demo \
    --cpu-request 1 \
    --memory-request 1Gi \
    --default-deny \
    --wait
fi

NS="$(kubectl get envlease "${LEASE_NAME}" -o jsonpath='{.status.namespace}' 2>/dev/null || true)"

cat <<EOF

========================================
KubeLease is ready.

Try:

  export PATH="${ROOT}/bin:\$PATH"
  kubectl kubelease list
  kubectl kubelease get ${LEASE_NAME}
  kubectl get namespace ${NS:-<lease-namespace>}
  kubectl get environmentleases

Expire the environment:

  kubectl kubelease expire ${LEASE_NAME}

Remove demo cluster:

  make demo-clean
========================================
EOF
