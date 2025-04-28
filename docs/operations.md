# Operations

## Install (kustomize)

```bash
make install          # CRDs
make deploy IMG=...   # controller
kubectl apply -k config/sourcewebhook   # optional webhook
```

## Install (Helm)

```bash
make install   # CRDs first
helm upgrade --install kubelease charts/kubelease \
  --namespace kubelease-system --create-namespace \
  --set image.repository=ghcr.io/hghukasyan/kubelease \
  --set image.tag=0.3.0 \
  --set webhook.github.enabled=true
```

Update the generated webhook Secret tokens before exposing Ingress.

## Demo

```bash
kubectl apply -f config/samples/platform_v1alpha1_environmentleasepolicy.yaml

kubectl kubelease create demo \
  --ttl 8h \
  --max-ttl 72h \
  --idle-ttl 2h \
  --policy preview-default

kubectl kubelease touch demo
kubectl kubelease list
kubectl kubelease expire demo
```

## Metrics (bounded cardinality)

| Metric | Labels |
|---|---|
| `kubelease_source_events_total` | `provider`, `action`, `result` |
| `kubelease_source_errors_total` | `provider`, `action` |
| `kubelease_idle_expirations_total` | — |
| `kubelease_manual_expirations_total` | — |
| `kubelease_policy_rejections_total` | — |

Never label by `lease_name`, `namespace`, `repo`, `user`, or PR number.

## Important Events

`EnvironmentProvisioned`, `LeaseRenewed`, `LeaseExpiring`, `LeaseExpired`,
`LeaseIdleExpired`, `SourceClosed`, `CleanupFailed` (plus cleanup started/completed).

## Probes

- Controller: `:8081` `/healthz` `/readyz`
- Webhook: `:8082` `/healthz` `/readyz` (ready requires token Secret)
