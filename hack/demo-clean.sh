#!/usr/bin/env bash
# Delete the Kind cluster created by hack/demo.sh.
set -euo pipefail

CLUSTER="${KIND_CLUSTER:-kubelease-demo}"

if ! command -v kind >/dev/null 2>&1; then
  echo "error: kind not found" >&2
  exit 1
fi

if kind get clusters 2>/dev/null | grep -qx "${CLUSTER}"; then
  echo "==> Deleting Kind cluster ${CLUSTER}"
  kind delete cluster --name "${CLUSTER}"
  echo "Done."
else
  echo "Kind cluster ${CLUSTER} not found — nothing to do."
fi
