#!/usr/bin/env bash
# Delete managed ResourceQuota and watch the controller recreate it.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
export PATH="${ROOT}/bin:${PATH}"
NAME="${1:-scenario-drift}"

kubectl kubelease create "${NAME}" --ttl 30m --max-ttl 2h --cpu-request 1 --memory-request 1Gi --wait
NS="$(kubectl get envlease "${NAME}" -o jsonpath='{.status.namespace}')"
echo "Namespace: ${NS}"
kubectl delete resourcequota -n "${NS}" --all --ignore-not-found
echo "Watch recreate (Ctrl-C when done):"
kubectl get resourcequota -n "${NS}" -w
