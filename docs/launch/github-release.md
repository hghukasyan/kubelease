# GitHub Release draft (v0.1.0)

## Highlights

First public release of KubeLease — a Kubernetes Operator for ephemeral
development environments with TTL, renewal, idle expiration, policies,
GitHub PR webhooks, and multi-cluster placement.

## Installation

See the release assets and README Quick Start. Prefer `make demo` to evaluate.

## Known limitations

- APIs are `v1alpha1` and may change before 1.0
- Helm chart does not own CRD lifecycle (`make install` first)
- Outbound durable notification destinations are not included
- No Grafana dashboard JSON is shipped
- Multi-cluster e2e is a scripted Kind path, not a required CI gate yet
