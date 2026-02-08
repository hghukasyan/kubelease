# EnvironmentLease

Cluster-scoped API: `platform.kubelease.io/v1alpha1` / `EnvironmentLease`.

## Minimal example

```yaml
apiVersion: platform.kubelease.io/v1alpha1
kind: EnvironmentLease
metadata:
  name: payment-pr
spec:
  ttl: 8h
  maxTTL: 72h
  renewable: true
  policyRef:
    name: preview-default
```

## Useful fields

| Spec | Purpose |
|---|---|
| `ttl` / `maxTTL` | Soft lifetime and hard cap from creation |
| `idleTTL` | Expire after inactivity (`touch` heartbeats) |
| `renewable` | Allow `extend` |
| `warnings` | Durations before expiry → Kubernetes Events |
| `policyRef` | Bind to `EnvironmentLeasePolicy` |
| `namespace` | Fixed name or `generateName` |
| `quota` / `limits` / `networkPolicy` | Managed Namespace constraints |
| `clusterRef` | Pin to one `ClusterTarget` |
| `placement.selector` | Soft placement among Ready targets (XOR `clusterRef`) |

## Status highlights

| Status | Meaning |
|---|---|
| `phase` | Lifecycle phase |
| `namespace` | Managed Namespace name |
| `expiresAt` / `effectiveExpiresAt` | Hard and effective expiry |
| `cluster` | Selected cluster (`local` or ClusterTarget name) |
| Conditions | Ready, cleanup, placement, identity, etc. |

Samples: [`config/samples/`](../config/samples/) · [`examples/`](../examples/)

Related: [policies](policies.md) · [idle expiration](idle-expiration.md) · [multicluster](multicluster.md)
