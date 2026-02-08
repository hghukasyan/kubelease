# Examples

Small, runnable manifests. Prefer `kubectl apply --dry-run=client -f` first.

| File | Topic |
|---|---|
| [01-basic.yaml](01-basic.yaml) | Minimal lease |
| [02-resource-limits.yaml](02-resource-limits.yaml) | Quota / LimitRange / NetworkPolicy |
| [03-policy.yaml](03-policy.yaml) | Policy + lease |
| [04-idle-expiration.yaml](04-idle-expiration.yaml) | idleTTL |
| [05-multicluster.yaml](05-multicluster.yaml) | Placement selector |
| [06-github-preview.yaml](06-github-preview.yaml) | PR-shaped lease |
| [07-notifications.yaml](07-notifications.yaml) | Honest note — outbound notifications are roadmap |

Also see [`config/samples/`](../config/samples/).
