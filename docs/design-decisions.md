# Design decisions

Interview-oriented notes on why KubeLease multi-cluster works the way it does.

## Why control-plane / target separation?

`EnvironmentLease`, policies, and `ClusterTarget` live on a **control cluster**.
Temporary Namespaces live on **target** clusters. That keeps:

* a single place for lease lifecycle / TTL / policy
* least-privilege remote credentials
* clear trust boundaries (who can create leases vs who can wipe Namespaces)

## Why reconcile remote state continuously?

Provisioning is not fire-and-forget. Drift (deleted Namespace/Quota) must be
healed. Expiration and finalizers need to observe actual remote presence.
One-shot create would leave orphans and lie about Ready.

## Why sticky placement?

Selector placement picks a ClusterTarget once and stores `status.cluster.name`.
Moving a live environment mid-flight risks duplicate databases, endpoints, and
cost. Reselection is allowed **only before** `status.namespace` exists.

## Why no live migration?

Migration looks like DR but creates two environments unless every workload is
stateless and carefully drained. KubeLease refuses that class of surprise.
Report `TargetClusterUnavailable` instead.

## Why finalizers on remote cleanup?

Without a finalizer, deleting an EnvironmentLease would drop the control-plane
object while remote Namespaces remain. Finalizers keep cleanup honest.
Default `RequireRemoteCleanup` prefers stuck-but-safe over silent orphans.

## Why verify cluster identity?

ClusterTarget **name** is not enough. Credential Secret rotation can retarget
the same CR at a different API. Cleanup must compare sticky remote identity
(kube-system UID) before deleting a Namespace name that might exist elsewhere.

## How client caching works

`ClusterTarget` → Secret → `rest.Config` → controller-runtime client.

Cache key includes Secret resourceVersion and a kubeconfig content hash.
Bounded LRU (default 32). Invalidate on Secret watch / target disable.

Credentials are never logged.

## New failure modes

* Target offline → shared outage backoff (many leases share one window)
* Identity drift → Ready=False, no follow
* Ownership mismatch → cleanup stops
* Soft capacity races → documented, not transactional admission

## Eventual consistency

Controllers requeue; watches enqueue assigned and Pending leases when
ClusterTargets change. Status is patched only when changed. Multi-replica
safety uses leader election on the controller; the HTTP webhook stays
stateless and does **not** require leadership to serve.

## Outages and work queues

Per-target outage tracker + controller-runtime rate limiting prevent 500 leases
from hammering a dead API every second. Per-target semaphores keep one slow
cluster from starving others under global MaxConcurrentReconciles.
