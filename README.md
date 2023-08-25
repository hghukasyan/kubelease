# KubeLease

Temporary Kubernetes environments with an expiration date.

KubeLease is a Kubernetes Operator that creates and manages ephemeral environments
from an `EnvironmentLease` custom resource. When the lease expires (or is deleted),
KubeLease safely cleans up the managed Namespace and its supporting resources.

## Why KubeLease?

Preview environments, CI sandboxes, and short-lived developer namespaces are easy
to create and hard to clean up. KubeLease treats environment lifetime as a first-class
Kubernetes API:

- Declarative TTL-based leases
- Consistent ResourceQuota / LimitRange / NetworkPolicy defaults
- Finalizer-based cleanup that survives controller restarts
- Level-based reconciliation (idempotent, restart-safe)

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
  | (no OR)   |   | LimitRange     |   | NetworkPolicy    |
  | labels +  |   | NetworkPolicy  |   | (OwnerReference) |
  | finalizer |   | (OwnerRef)     |   +------------------+
  +-----------+   +----------------+
```

**Design notes**

- `EnvironmentLease` is **cluster-scoped** because it manages Namespaces.
- The managed **Namespace has no OwnerReference**. Ownership is tracked with
  labels (`app.kubernetes.io/managed-by=kubelease`, `kubelease.io/lease=<name>`)
  and cleaned up via the lease finalizer. This avoids GC races with Namespace
  cascading deletion.
- Resources **inside** the Namespace use OwnerReferences to the lease.

## Installation

```bash
# Install CRDs
make install

# Deploy the controller (image must be available to the cluster)
make deploy IMG=<your-registry>/kubelease:tag
```

Or apply generated manifests from `config/`.

## Quick Start

```bash
kubectl apply -f config/samples/platform_v1alpha1_environmentlease.yaml
kubectl get environmentleases
kubectl get ns -l app.kubernetes.io/managed-by=kubelease
```

## EnvironmentLease example

```yaml
apiVersion: platform.kubelease.io/v1alpha1
kind: EnvironmentLease
metadata:
  name: payment-api-pr-1842
spec:
  ttl: 8h
  owner:
    name: hayk
    team: payments
  namespace:
    generateName: preview-
    labels:
      environment: preview
      application: payments
  quota:
    requests:
      cpu: "2"
      memory: 4Gi
    limits:
      cpu: "4"
      memory: 8Gi
  limits:
    default:
      cpu: 500m
      memory: 512Mi
    defaultRequest:
      cpu: 100m
      memory: 128Mi
  networkPolicy:
    defaultDeny: true
```

## Lifecycle

```text
Created
  -> Pending/Provisioning
  -> Active  (RequeueAfter until expiresAt)
  -> Expiring/Cleaning (TTL elapsed or CR deleted)
  -> Expired (namespace gone)
  -> object removed (finalizer cleared on delete)
```

TTL timestamps:

- `status.createdAt` is set once and never reset on controller restart
- `status.expiresAt = createdAt + spec.ttl`
- Changing `spec.ttl` recalculates `expiresAt` from the sticky `createdAt`

## Status / Conditions

| Field | Meaning |
|---|---|
| `status.phase` | Human-readable summary (`Pending`, `Provisioning`, `Active`, `Expiring`, `Cleaning`, `Expired`, `Failed`) |
| `status.namespace` | Managed Namespace name |
| `status.createdAt` / `expiresAt` | Lease window |
| `status.observedGeneration` | Last processed `metadata.generation` |
| `status.conditions` | Authoritative machine-readable state (`Ready`, `EnvironmentCreated`, `Cleanup`) |

## Metrics

KubeLease registers:

| Metric | Type | Description |
|---|---|---|
| `kubelease_active_leases` | Gauge | Best-effort count of Active leases |
| `kubelease_expired_leases_total` | Counter | Leases that reached TTL expiration |
| `kubelease_cleanup_failures_total` | Counter | Failed cleanup attempts |
| `kubelease_provision_failures_total` | Counter | Failed provisioning attempts |

Controller-runtime workqueue / reconcile metrics are also exposed on the metrics endpoint.

## Security model

- Least-privilege ClusterRole (leases, namespaces, quotas, limitranges, networkpolicies, events)
- Never manages `default`, `kube-system`, `kube-public`, `kube-node-lease`
- Cleanup refuses namespaces that do not carry matching KubeLease labels
- Leader election enabled in production manifests (`--leader-elect`)

## Leader election

Multiple controller replicas can run safely with leader election (enabled by default
in `config/manager`). Only the leader reconciles.

Local development without election:

```bash
go run ./cmd/main.go --leader-elect=false --metrics-bind-address=:8080 --metrics-secure=false
```

Production: keep `--leader-elect` enabled.

## Development

```bash
# Generate manifests and code
make manifests generate

# Format / vet
make fmt vet

# Unit + envtest
make test

# Run locally against the current kubeconfig context
make install
make run
```

### Testing with Kind

```bash
kind create cluster --name kubelease
make docker-build IMG=kubelease:dev
kind load docker-image kubelease:dev --name kubelease
make deploy IMG=kubelease:dev
kubectl apply -f config/samples/platform_v1alpha1_environmentlease.yaml
kubectl get environmentleases -o wide
kubectl get ns -l kubelease.io/lease=payment-api-pr-1842
```

### Example kubectl workflow

```bash
# Create
kubectl apply -f config/samples/platform_v1alpha1_environmentlease.yaml

# Inspect
kubectl get envlease payment-api-pr-1842 -o yaml
kubectl describe envlease payment-api-pr-1842

# Delete (triggers finalizer cleanup of the Namespace)
kubectl delete envlease payment-api-pr-1842
```

## Roadmap

Phase 1 (this release): CRD, controller, Namespace/Quota/LimitRange/NetworkPolicy,
TTL expiration, finalizers, status/conditions, metrics, tests, CI.

Later phases (not implemented yet):

- Slack / GitHub integrations
- Idle TTL / Prometheus activity detection
- Admission webhooks and external lifecycle hooks
- UI and kubectl plugin
- Cloud provider integrations
- Multi-cluster

## Contributing

1. Fork and create a feature branch
2. Run `make test` and `make lint`
3. Keep reconciles level-based and idempotent
4. Open a PR with context and test notes

## License

Apache License 2.0. See [LICENSE](LICENSE).
