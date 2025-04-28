# Security

## Threat model (summary)

| Risk | Mitigation |
|---|---|
| Webhook deletes Namespaces | Webhook RBAC has **no** `namespaces` verbs |
| GitHub/token leakage onto CRs | Secrets only in K8s Secrets; annotations audited |
| Quota / maxTTL bypass via webhook | Unknown JSON fields rejected; create uses policy only |
| Protected system namespaces | Controller refuses `default` / `kube-*` |
| Foreign Namespace adoption | Requires matching lease name **and** UID annotation |
| Signature forgery | HMAC SHA-256 (`X-Hub-Signature-256`) constant-time compare |
| Generic webhook auth bypass | Bearer / header token vs Secret; constant-time compare |
| Team spoofing | Generic webhook trusts authenticated caller metadata; GitHub uses payload sender/owner |
| SSRF | Integration service does not make outbound GitHub/API calls for PR lifecycle |
| Event storms | Warning delivery ledger; first-expiry / first-delete event guards |
| Destructive cleanup | Finalizer + ownership checks before Namespace delete |

## RBAC split

```text
webhook service
  → create/update/delete EnvironmentLease (+ status for touch)
  → get EnvironmentLeasePolicy
  → get own token Secret

controller
  → manage Namespace / ResourceQuota / LimitRange / NetworkPolicy
  → EnvironmentLease status + finalizers
```

## Hardening checklist

- [ ] Rotate webhook and GitHub secrets
- [ ] Restrict Ingress to GitHub/CI source IPs when exposed
- [ ] Keep webhook replicas behind ClusterIP unless Ingress is required
- [ ] Run containers as non-root with dropped capabilities
- [ ] Keep `govulncheck ./...` green in CI
