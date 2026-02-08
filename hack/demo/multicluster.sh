#!/usr/bin/env bash
# Requires a Ready ClusterTarget. See hack/kind-multicluster-e2e.md.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
export PATH="${ROOT}/bin:${PATH}"

echo "Listing ClusterTargets:"
kubectl kubelease cluster list
echo
echo "Apply examples/05-multicluster.yaml after labels match a Ready target."
kubectl get clustertargets -o wide || true
