# Idle expiration

When `spec.idleTTL` (or policy default) is set:

```text
effectiveExpiration = min(hardExpiration, lastActivityAt + idleTTL)
```

## Status fields

| Field | Meaning |
|---|---|
| `status.lastActivityAt` | Last heartbeat (status; initialized to `createdAt`) |
| `status.idleExpiresAt` | `lastActivityAt + idleTTL` (capped to hard TTL) |
| `status.effectiveExpiresAt` | Deadline used for scheduling and expiry |
| `status.expirationReason` | `TTLExpired`, `IdleTimeout`, `ManualExpiration`, `SourceClosed` |

## Heartbeats

```bash
kubectl kubelease touch demo
```

Touch extends idle lifetime only. It never extends hard TTL / maxTTL.

Activity belongs in **status**, not spec. Heartbeats require `idleTTL > 0`.

## Controller restart

`lastActivityAt` is persisted on the lease status. After restart, the controller
recomputes derived deadlines and expires immediately if already past
`effectiveExpiresAt`.
