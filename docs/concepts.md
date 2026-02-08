# Concepts

## EnvironmentLease

Cluster-scoped custom resource representing a temporary environment.
The controller provisions a Namespace (and optional ResourceQuota, LimitRange,
NetworkPolicy) on a target cluster, tracks TTL / idle expiry, and cleans up
when the lease ends.

## EnvironmentLeasePolicy

Optional platform guardrails: max TTL, quotas, NetworkPolicy requirements,
placement constraints. Referenced via `spec.policyRef`.

## ClusterTarget

Registers a remote Kubernetes cluster (credentials via Secret `secretRef`).
Leases place onto a target with `spec.clusterRef` or `spec.placement.selector`.
Omitting both uses the local (control) cluster.

## Phases

Typical progression:

```text
Pending → Provisioning → Active → Expiring → Cleaning → Expired
```

`Failed` / degraded Conditions surface placement, identity, ownership, or
remote outage problems. See [troubleshooting](troubleshooting.md).

## Ownership

Managed Namespaces are labeled and annotated with the lease name and UID.
Cleanup refuses to delete Namespaces that fail ownership checks.

## Sources

- CLI / `kubectl apply`
- Generic HTTP webhook (`/v1/leases`)
- GitHub PR webhooks (`/v1/github/hooks`)

Deeper reading: [architecture](architecture.md) · [design decisions](design-decisions.md)
