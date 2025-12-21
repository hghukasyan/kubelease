# Multi-cluster security

Threat model and destructive-operation safety for KubeLease remote clusters.

## Trust boundary

```text
User / CI
  ↓
Control Cluster API
  ↓
EnvironmentLease / ClusterTarget / Policy
  ↓
KubeLease Controller (leader-elected)
  ↓
Credential Secret (kubeconfig)
  ↓
Remote Cluster API
  ↓
Namespace / Quota / LimitRange / NetworkPolicy
```

The webhook path only mutates `EnvironmentLease` on the control cluster. It must
**not** read remote kubeconfig Secrets or delete Namespaces.

## Threats and mitigations

| Threat | Mitigation |
|---|---|
| Stolen kubeconfig | Least-privilege remote RBAC; rotate Secrets; cache invalidates on Secret RV |
| Malicious ClusterTarget | Restrict who can create ClusterTargets / secretRefs; audit |
| Compromised control cluster | Attacker can create leases and use any Secret the controller can read — treat control plane as high trust |
| Excessive remote RBAC | Documented remote ClusterRole (not cluster-admin) |
| Wrong-target deletion after Secret swap | Sticky `status.remoteIdentity` (kube-system UID) on ClusterTarget + lease; cleanup verifies live identity |
| Stale cached clients | Fingerprint includes Secret resourceVersion + kubeconfig hash; LRU cache |
| Secret values in logs/metrics | Never log kubeconfig bytes; metrics carry only target names |
| Cluster spoofing / name reuse | Lease stores `targetUID` + `remoteIdentity`; Namespace annotations carry the same |
| Accidental force cleanup | `kubelease.io/force-cleanup-acknowledged=true` is explicit and awkward |

## Sticky remote identity

On first successful health probe, ClusterTarget records:

```yaml
status:
  remoteIdentity: <kube-system Namespace UID>
```

If credentials later point at a **different** cluster installation:

```text
Ready=False
Reason=IdentityDrift
```

KubeLease will **not** silently follow the new cluster. Operators must create a
new ClusterTarget (or clear identity deliberately after acknowledging data loss).

Leases capture the same fingerprint at provision time:

```yaml
status:
  cluster:
    name: dev-east
    targetUID: ...
    remoteIdentity: ...
```

Namespaces are annotated:

```text
kubelease.io/lease-uid
kubelease.io/control-cluster-id
kubelease.io/target-identity
```

## Cleanup safety

Before deleting a remote Namespace, KubeLease verifies:

1. Live remote identity matches `status.cluster.remoteIdentity`
2. Namespace labels/annotations match the lease UID and control-cluster-id

On mismatch:

```text
Cleanup=False
Reason=OwnershipMismatch
```

Deletion **stops** (no hot-loop). Fix the identity or, only after deliberate
review, set:

```bash
kubectl annotate environmentlease NAME \
  kubelease.io/force-cleanup-acknowledged=true
```

This skips remote delete and allows finalizer removal. It can orphan remote
resources — treat it as a break-glass action.

## Permanent cluster loss

KubeLease **cannot** clean up a cluster that no longer exists. With default
`RequireRemoteCleanup`, the lease finalizer stays until:

* the cluster returns, or
* BestEffort cleanup mode is set, or
* force-cleanup-acknowledged is applied

Do not expect magical cross-cluster GC.

## Credential rotation

Supported: update the kubeconfig Secret for the **same** remote cluster.

* Client cache invalidates (Secret RV / content hash)
* Identity must still match sticky `remoteIdentity`

Not supported silently: pointing the same ClusterTarget at a different cluster.

## Cardinality note for metrics

`kubelease_cluster_health{cluster=...}` and related cluster-labelled metrics
assume a **small, operator-configured** set of ClusterTargets. Do not use
unbounded lease/namespace/PR labels.
