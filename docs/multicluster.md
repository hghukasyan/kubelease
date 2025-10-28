# Multi-cluster

KubeLease can provision temporary environments on remote Kubernetes clusters
while keeping `EnvironmentLease` (and related policy/notification CRs) on the
control-plane cluster.

```text
					+----------------------+
					| KubeLease Control     |
					| Cluster               |
					+----------+-----------+
							   |
			  +----------------+----------------+
			  |                |                |
			  v                v                v
		Cluster A          Cluster B        Cluster C
		dev-us-east        dev-eu           gpu-test
```

## Placement

Leases need not hard-code `clusterRef`. Prefer label-based placement:

```yaml
apiVersion: platform.kubelease.io/v1alpha1
kind: ClusterTarget
metadata:
  name: dev-east
  labels:
    kubelease.io/region: us-east
    kubelease.io/tier: dev
    kubelease.io/gpu: "false"
spec:
  credentials:
    secretRef:
      name: dev-east-kubeconfig
      namespace: kubelease-system
  maxActiveLeases: 50   # soft capacity (best-effort, not transactional)
---
apiVersion: platform.kubelease.io/v1alpha1
kind: EnvironmentLease
metadata:
  name: payment-pr
spec:
  ttl: 8h
  placement:
    selector:
      matchLabels:
        kubelease.io/region: us-east
        kubelease.io/tier: dev
  namespace:
    generateName: preview-
```

### Precedence

```text
clusterRef set     → exact target
placement set      → deterministic select among matching Ready targets
neither            → local control cluster
```

`clusterRef` and `placement` are **mutually exclusive** (CEL + controller validation).

### Sticky selection

Chosen target is written to `status.cluster.name` and reused on every reconcile.

| Stage | Behavior |
|---|---|
| Before Namespace exists | Unhealthy/disabled selection may be **reselected** |
| After Namespace exists | Selection is **sticky** — no automatic migration |

A live environment is never copied to another cluster. If the target fails after
provisioning: `Ready=False` / `TargetClusterUnavailable`.

### Algorithm

1. Filter enabled + Ready targets
2. Apply lease + policy label selectors (`metav1.LabelSelector`)
3. Soft-capacity filter (`activeLeases < maxActiveLeases`)
4. Sort by name; pick via stable hash of lease UID

Hashing spreads load across matches while remaining deterministic for a lease.
Sorted-first would pile onto the alphabetically first target.

### Soft capacity

`maxActiveLeases` / `status.capacity.activeLeases` are **soft**. Concurrent
reconciles can race past the ceiling — do not treat them as hard admission.

### Policy

`EnvironmentLeasePolicy.spec.placement.selector` constrains both explicit
`clusterRef` and lease placement. When a lease omits both, the policy selector
is used as the default placement.

### No match

`Phase=Pending`, `Ready=False`, `reason=NoMatchingCluster`, requeue with backoff.
A new matching ClusterTarget recovers automatically.

### CLI

```bash
kubectl kubelease cluster list
kubectl kubelease cluster get dev-east
kubectl kubelease get payment-pr   # shows Cluster / Placement
```

---

## Design choices

| Topic | Choice |
|---|---|
| Scope | `ClusterTarget` is **cluster-scoped** (platform infrastructure) |
| Credentials | Secret reference only — never embed kubeconfig in the CR |
| Auth today | kubeconfig in a Secret (`credentials.secretRef`) |
| Future auth | SA token / workload identity can extend `ClusterCredentials` without API break for Secret refs |
| Local default | omit `clusterRef` and `placement` → local control cluster |
| Placement | `metav1.LabelSelector` on ClusterTarget scheduling labels; sticky in status |
| Capacity | soft `maxActiveLeases` only (not CPU/memory packing) |
| Cleanup | default `cleanupPolicy.mode=RequireRemoteCleanup` — finalizer stays until remote Namespace is gone |

## ClusterTarget

```yaml
apiVersion: platform.kubelease.io/v1alpha1
kind: ClusterTarget
metadata:
  name: dev-east
spec:
  credentials:
    secretRef:
      name: dev-east-kubeconfig
      namespace: kubelease-system   # required (ClusterTarget is cluster-scoped)
      key: kubeconfig             # default
  labels:
    region: us-east
    environment: development
  enabled: true
```

Create the Secret (example):

```bash
kubectl -n kubelease-system create secret generic dev-east-kubeconfig \
  --from-file=kubeconfig=/path/to/dev-east.kubeconfig
```

Status conditions: `Ready`, `Authenticated`, `Reachable`, plus `kubernetesVersion`.
Health probes run on a **scheduled** interval (~5m) and on Secret/target changes —
not on every lease reconcile.

## EnvironmentLease targeting

```yaml
apiVersion: platform.kubelease.io/v1alpha1
kind: EnvironmentLease
metadata:
  name: payment-pr
spec:
  clusterRef:
    name: dev-east
  ttl: 8h
  namespace:
    generateName: preview-
```

Status:

```yaml
status:
  cluster:
    name: dev-east
  namespace: preview-r82jx
  conditions:
    - type: Ready
      status: "True"
    - type: TargetClusterReady
      status: "True"
```

Desired builders (`Namespace`, `ResourceQuota`, `LimitRange`, `NetworkPolicy`) are
unchanged; only the **apply client** switches to the target cluster.

Remote in-namespace resources do **not** OwnerReference the hub `EnvironmentLease`
(cross-cluster OwnerRefs are invalid). Ownership remains labels +
`kubelease.io/lease-uid`.

## Failure behavior

| Failure | Lease behavior |
|---|---|
| Target missing / disabled / offline / bad TLS / RBAC denied | `Ready=False`, `TargetClusterReady=False`, reason `TargetClusterUnavailable` (or more specific), **backoff** — not treated as destroyed |
| Remote Namespace deleted while lease Active | Recreated on next reconcile (drift recovery; remote leases also requeue periodically) |

## Cleanup and finalizers

Default **`RequireRemoteCleanup`**: if the remote cluster is offline during delete/expire,
KubeLease **keeps** the lease finalizer and sets `RemoteCleanupBlocked`. Do not
force-remove the finalizer unless operators have confirmed the remote Namespace
is gone or acceptable to orphan.

Optional:

```yaml
spec:
  cleanupPolicy:
    mode: BestEffort   # may orphan remote Namespaces
```

### Stuck finalizer recovery (operations)

1. Confirm remote Namespace deleted (or cluster permanently gone).
2. Optionally switch to `BestEffort` and reconcile, **or**
3. Manually remove the lease finalizer only after accepting orphan risk:

```bash
kubectl patch environmentlease payment-pr --type=json \
  -p='[{"op":"remove","path":"/metadata/finalizers"}]'
```

## ClusterTarget deletion

A finalizer blocks deletion while any `EnvironmentLease.spec.clusterRef` still
points at the target. Drain or retarget leases first.

## Control-plane vs remote RBAC

### Control cluster (KubeLease controller)

- Full CRUD on `EnvironmentLease` / status / finalizers
- get/list/watch `ClusterTarget`, `EnvironmentLeasePolicy`
- get/list/watch **credential Secrets** (prefer Secrets in the controller namespace)
- Local Namespace/RQ/LR/NP verbs (for local leases)

Do **not** grant the webhook SA remote credentials or Namespace delete.

### Remote cluster (kubeconfig identity)

Least privilege — **not** `cluster-admin`. Example:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: kubelease-remote
rules:
  - apiGroups: [""]
    resources: ["namespaces"]
    verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
  - apiGroups: [""]
    resources: ["resourcequotas", "limitranges"]
    verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
  - apiGroups: ["networking.k8s.io"]
    resources: ["networkpolicies"]
    verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: kubelease-remote
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: kubelease-remote
subjects:
  - kind: ServiceAccount
    name: kubelease-remote
    namespace: kubelease-remote
```

Point the kubeconfig at that ServiceAccount token.

## Kind dual-cluster smoke

```bash
kind create cluster --name kubelease-control
kind create cluster --name kubelease-target
# install CRDs + controller on control
# create SA+RBAC on target, export kubeconfig Secret on control
# apply ClusterTarget + EnvironmentLease with clusterRef
# kubectl --context kind-kubelease-target get ns
```

## Metrics

| Metric | Labels |
|---|---|
| `kubelease_cluster_targets` | `ready=true\|false` |
| `kubelease_remote_operations_total` | `operation`, `result` |
| `kubelease_cluster_connection_failures_total` | (none) |

Target **name** is intentionally omitted from labels to keep cardinality bounded.
