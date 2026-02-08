#!/usr/bin/env bash
# Recordable scenario: create → list → get → expire
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
export PATH="${ROOT}/bin:${PATH}"
NAME="${1:-scenario-basic}"

kubectl kubelease create "${NAME}" --ttl 20m --max-ttl 1h --wait || true
kubectl kubelease list
kubectl kubelease get "${NAME}"
kubectl get environmentleases "${NAME}" -o wide
kubectl kubelease expire "${NAME}"
