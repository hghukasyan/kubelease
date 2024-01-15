# KubeLease

Temporary Kubernetes environments with an expiration date.

KubeLease is a Kubernetes Operator that creates and manages ephemeral environments
from an `EnvironmentLease` custom resource. When the lease expires (or is deleted),
KubeLease safely cleans up the managed Namespace and its supporting resources.

## Why KubeLease?

Preview environments, CI sandboxes, and short-lived developer namespaces are easy
to create and hard to clean up. KubeLease treats environment lifetime as a first-class
Kubernetes API with renewal, max lifetime, expiration warnings, and a kubectl plugin.

## Use cases

- Per-PR preview namespaces
- Time-boxed developer scratch environments
- CI job isolation with automatic teardown
- Platform-enforced default-deny networking for ephemeral workloads

## Architecture

```text
                 +----------------------+
                 |  EnvironmentLease    |  (cluster-scoped CR)
                 |  platform.kubelease  |
                 +----------+-----------+
                            |
                            | reconciles
                            v
                 +----------------------+
                 | kubelease-controller |
                 | (leader-elected)     |
                 +----------+-----------+
                            |
        +-------------------+-------------------+
        |                   |                   |
        v                   v                   v
  +-----------+   +----------------+   +------------------+
  | Namespace |   | ResourceQuota  |   | LimitRange       |
  | labels +  |   | LimitRange     |   | NetworkPolicy    |
  | lease-uid |   | NetworkPolicy  |   | (OwnerReference) |
  | (no OR)   |   | (OwnerRef)     |   +------------------+
  +-----------+   +----------------+
```

**Design notes**

- `EnvironmentLease` is **cluster-scoped** because it manages Namespaces.
- The managed **Namespace has no OwnerReference**. Ownership uses labels
  (`app.kubernetes.io/managed-by=kubelease`, `kubelease.io/lease=<name>`) plus
  annotation `kubelease.io/lease-uid=<uid>`, cleaned up via the lease finalizer.
- Resources **inside** the Namespace use OwnerReferences to the lease.
- **Renewal model:** increase `spec.ttl`. Controller sets
  `status.expiresAt = createdAt + ttl`, clamped to `createdAt + maxTTL`.

## Lifecycle

```text
Create
  |
  v
Provisioning
  |
  v
Active <---------+
  |              |
  |            Renew (increase spec.ttl)
  |              |
  +--------------+
  |
  | warnings (LeaseExpiring events)
  v
Expiring
  |
  v
Cleaning
  |
  v
Expired
```

## CLI installation

```bash
go install github.com/hghukasyan/kubelease/cmd/kubectl-kubelease@latest
# or: make cli && export PATH="$PWD/bin:$PATH"

kubectl kubelease --help
```

## Create environment

```bash
kubectl kubelease create payment-pr \
  --ttl 8h \
  --max-ttl 72h \
  --owner hayk \
  --team payments \
  --cpu-request 2 \
  --memory-request 4Gi \
  --default-deny \
  --warning 1h \
  --warning 15m
```

## List / get / extend / expire

```bash
kubectl kubelease list
kubectl kubelease get payment-pr
kubectl kubelease extend payment-pr --for 4h
kubectl kubelease expire payment-pr
```

`expire` deletes the `EnvironmentLease` CR. The controller finalizer performs cleanup.
The CLI never deletes Namespaces directly.

## Installation (controller)

```bash
make install
make deploy IMG=<your-registry>/kubelease:tag
```

## Status / Conditions

| Field | Meaning |
|---|---|
| `status.phase` | `Pending`, `Provisioning`, `Active`, `Expiring`, `Cleaning`, `Expired`, `Failed` |
| `status.namespace` | Managed Namespace name |
| `status.createdAt` | Sticky lease start (from `metadata.creationTimestamp`) |
| `status.expiresAt` | `createdAt + ttl`, clamped to max |
| `status.maximumExpiresAt` | `createdAt + maxTTL` |
| `status.warningsDelivered` | Warning keys already emitted (restart-safe) |
| Conditions | `Ready`, `Expiring`, `Cleanup`, `Degraded` |

## Field ownership

| Resource | KubeLease owns | Users may add |
|---|---|---|
| Namespace | management labels, `kubelease.io/lease-uid` | extra labels/annotations (merged) |
| ResourceQuota | `spec.hard`, management labels | — |
| LimitRange | container limit item, labels | — |
| NetworkPolicy | selector, policyTypes, empty ingress/egress | — |

Manual Namespace delete while Active: controller **recreates the same** `status.namespace` name.

## Metrics

| Metric | Type |
|---|---|
| `kubelease_leases{phase=...}` | Gauge (bounded phase label) |
| `kubelease_leases_created_total` | Counter |
| `kubelease_leases_expired_total` | Counter |
| `kubelease_renewals_total` | Counter |
| `kubelease_cleanup_failures_total` | Counter |
| `kubelease_provision_failures_total` | Counter |
| `kubelease_warning_events_total` | Counter |

No high-cardinality labels (`lease_name`, `owner`, etc.).

## Security model

- Least-privilege ClusterRole
- Never manages `default`, `kube-system`, `kube-public`, `kube-node-lease`
- Cleanup requires matching lease name **and** UID annotation
- Refuses to adopt Namespaces owned by a different lease
- Leader election enabled in production manifests

## Development

```bash
make manifests generate
make test          # unit + envtest
make test-unit
make test-integration
make lint
make cli
make run           # controller against current kubeconfig
```

### Kind workflow

```bash
kind create cluster --name kubelease-dev
make install
make run
# other terminal:
make cli && export PATH="$PWD/bin:$PATH"
kubectl kubelease create demo --ttl 30m --max-ttl 2h --owner hayk --cpu-request 1 --memory-request 1Gi --default-deny
kubectl kubelease list
kubectl kubelease extend demo --for 15m
kubectl delete resourcequota kubelease-quota -n "$(kubectl get envlease demo -o jsonpath='{.status.namespace}')"
# watch it recreate
kubectl kubelease expire demo
```

## Roadmap

- Phase 3+: Slack / GitHub integrations, idle TTL, multi-cluster, UI

## Contributing

1. Fork and create a feature branch
2. Run `make test` and `make lint`
3. Keep reconciles level-based and idempotent
4. Open a PR with context and test notes

## License

Apache License 2.0. See [LICENSE](LICENSE).
