# Observability

## Metrics

Prometheus metrics are exposed from the controller (see default manifests /
ServiceMonitor under `config/prometheus/`).

Examples (bounded label cardinality — no lease names or PR numbers):

- `kubelease_leases{phase=...}`
- `kubelease_leases_created_total` / `kubelease_leases_expired_total`
- `kubelease_renewals_total`
- `kubelease_cleanup_failures_total` / `kubelease_provision_failures_total`
- `kubelease_warning_events_total`
- `kubelease_source_events_total` / `kubelease_webhook_requests_total`
- Multi-cluster metrics include a carefully bounded cluster label where applicable

There is **no** Grafana dashboard shipped in this repository yet.
Do not assume a dashboard JSON exists.

## Events

Warning and lifecycle Events are emitted on the `EnvironmentLease`
(e.g. provisioning, expiring, expired).

```bash
kubectl describe environmentlease <name>
kubectl get events --field-selector involvedObject.name=<name>
```

## Status Conditions

Inspect Conditions for actionable reasons (`NoMatchingCluster`,
`TargetClusterUnavailable`, `PolicyRejected`, `OwnershipMismatch`, etc.).

See [troubleshooting](troubleshooting.md).
