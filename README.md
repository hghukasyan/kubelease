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

## List / get / extend / touch / expire

```bash
kubectl kubelease list
kubectl kubelease get payment-pr
kubectl kubelease extend payment-pr --for 4h
kubectl kubelease touch payment-pr
kubectl kubelease expire payment-pr
```

`touch` records activity (`status.lastActivityAt`) and extends idle expiration when
`idleTTL` is set. It never extends the hard TTL / maxTTL.

`expire` deletes the `EnvironmentLease` CR. The controller finalizer performs cleanup.
The CLI never deletes Namespaces directly.

## Idle expiration

When `spec.idleTTL` is set:

```text
effectiveExpiration = min(hardExpiration, lastActivityAt + idleTTL)
```

- `status.expiresAt` — hard TTL (`createdAt + ttl`)
- `status.lastActivityAt` — last heartbeat (status; not spec)
- `status.idleExpiresAt` — `lastActivityAt + idleTTL` (capped to hard TTL)
- `status.effectiveExpiresAt` — deadline used for scheduling and expiry
- `status.expirationReason` — `TTLExpired`, `IdleTimeout`, `ManualExpiration`, or `SourceClosed`

```bash
kubectl kubelease create payment-pr --ttl 8h --idle-ttl 30m
kubectl kubelease touch payment-pr
```

## EnvironmentLeasePolicy

Reusable cluster-scoped policies provide defaults and hard limits:

```yaml
apiVersion: platform.kubelease.io/v1alpha1
kind: EnvironmentLeasePolicy
metadata:
  name: preview-default
spec:
  ttl:
    default: 8h
    maximum: 72h
  quota:
    maxCPU: "4"
    maxMemory: 8Gi
  networkPolicy:
    defaultDenyRequired: true
```

Leases reference a policy with `spec.policyRef`. Omitted fields take policy
defaults. Values that violate hard limits are **rejected** (status Failed /
`PolicyViolation`) — they are never silently clamped.

```yaml
spec:
  policyRef:
    name: preview-default
```

`status.effective` records the resolved TTL, maxTTL, renewable, and defaultDeny.

## Generic webhook source

KubeLease exposes an optional authenticated HTTP integration (separate from the
controller reconciler) for external CI systems:

```text
External CI → HTTP webhook → EnvironmentLease → Controller
```

```bash
make webhook
# deploy optional manifests (after setting a real token + default policy):
kubectl apply -k config/sourcewebhook
```

```bash
curl -sS -X POST http://kubelease-webhook.kubelease-system.svc/v1/leases \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"action":"create","name":"feature-482","owner":"payments"}'
```

Supported actions: `create`, `expire`, `touch`.

Callers cannot set `namespace`, `maxTTL`, quotas, or network settings — those come
from the configured `EnvironmentLeasePolicy` (`--default-policy`). Unknown JSON
fields are rejected. Requests are idempotent via `requestId` / `Idempotency-Key`.

## GitHub pull request integration

Built on the same webhook service. GitHub signatures are verified with an HMAC
secret stored only in a Kubernetes Secret (never on `EnvironmentLease` specs).

| Event | Behavior |
|---|---|
| `pull_request.opened` | Ensure lease exists |
| `pull_request.reopened` | Ensure active lease (create if absent) |
| `pull_request.closed` | Request lease expiration |
| `pull_request` closed + merged | Request lease expiration (`SourceClosed`) |

Deterministic identity:

```text
my-company/payments-api PR #1842 → EnvironmentLease/payments-api-pr-1842
```

```bash
# Configure GitHub webhook: POST /v1/github/hooks
# Secret: value of github-webhook-secret in the webhook Secret
curl -sS -X POST http://kubelease-webhook.kubelease-system.svc/v1/github/hooks \
  -H "X-GitHub-Event: pull_request" \
  -H "X-GitHub-Delivery: $DELIVERY_ID" \
  -H "X-Hub-Signature-256: sha256=..." \
  -d @payload.json
```

Repo → policy mapping uses `--github-repo-policies` JSON. The webhook deletes the
lease CR only; the controller performs Namespace cleanup.

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
| `status.expiresAt` | Hard TTL: `createdAt + ttl` |
| `status.maximumExpiresAt` | `createdAt + maxTTL` |
| `status.lastActivityAt` | Last heartbeat (initialized to `createdAt`) |
| `status.idleExpiresAt` | `lastActivityAt + idleTTL` (capped to hard TTL) |
| `status.effectiveExpiresAt` | `min(expiresAt, idleExpiresAt)` |
| `status.expirationReason` | Why the lease expired |
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
| `kubelease_webhook_requests_total` | Counter (`action`, `result`) |
| `kubelease_webhook_request_duration_seconds` | Histogram |

No high-cardinality labels (`lease_name`, `owner`, etc.).

## Security model

- Least-privilege ClusterRole
- Never manages `default`, `kube-system`, `kube-public`, `kube-node-lease`
- Cleanup requires matching lease name **and** UID annotation
- Refuses to adopt Namespaces owned by a different lease
- Leader election enabled in production manifests
- Webhook auth uses a Kubernetes Secret token; body size and timeouts are enforced
- Webhook create path cannot override policy hard limits

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

- Phase 3+: Slack notifications, multi-cluster, UI

## Contributing

1. Fork and create a feature branch
2. Run `make test` and `make lint`
3. Keep reconciles level-based and idempotent
4. Open a PR with context and test notes

## License

Apache License 2.0. See [LICENSE](LICENSE).
