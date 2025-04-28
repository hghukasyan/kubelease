# Policies

`EnvironmentLeasePolicy` is cluster-scoped and provides:

- defaults for omitted lease fields
- hard limits that are **rejected** (never silently clamped)

## Example

```yaml
apiVersion: platform.kubelease.io/v1alpha1
kind: EnvironmentLeasePolicy
metadata:
  name: preview-default
spec:
  ttl:
    default: 8h
    maximum: 72h
  idleTTL:
    default: 30m
    maximum: 2h
  quota:
    maxCPU: "4"
    maxMemory: 8Gi
  networkPolicy:
    defaultDenyRequired: true
```

## Resolution rules

1. Unset lease field → policy default (if any)
2. Set lease field that violates min/max / force / quota ceiling → `Failed` + `PolicyViolation`
3. Resolution is deterministic and re-evaluated every reconcile

## What integrations may set

Webhook/GitHub create paths set only:

- `policyRef` (from server config / repo map)
- owner metadata
- namespace `generateName`
- source labels/annotations

They cannot set exact namespace names, maxTTL, quotas, or network settings from the caller payload.
