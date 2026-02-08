#!/usr/bin/env bash
# Short TTL to observe expiration (use a longer sleep when recording).
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
export PATH="${ROOT}/bin:${PATH}"
NAME="${1:-scenario-expire}"

kubectl kubelease create "${NAME}" --ttl 2m --max-ttl 10m --wait
kubectl kubelease list
echo "Waiting for TTL expiration (or expire manually)..."
kubectl kubelease expire "${NAME}"
kubectl kubelease list || true
