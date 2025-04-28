# Generic webhook integration

Binary: `kubelease-webhook`  
Endpoint: `POST /v1/leases`

## Auth

Shared token from a Kubernetes Secret:

- `Authorization: Bearer <token>` or
- `X-KubeLease-Token: <token>`

## Actions

| action | Effect |
|---|---|
| `create` | Create `EnvironmentLease` (policy-backed) |
| `expire` | Delete lease CR (controller cleans Namespace) |
| `touch` | Update `status.lastActivityAt` |

## Example

```bash
curl -sS -X POST http://kubelease-webhook/v1/leases \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"action":"create","name":"feature-482","owner":"payments"}'
```

Idempotency: `requestId` body field or `Idempotency-Key` header.

## Limits

- Max body size (default 64KiB)
- Request/read/write timeouts
- Unknown JSON fields rejected (blocks namespace/maxTTL/quota smuggling)
- `/healthz` and `/readyz`

## Metrics

- `kubelease_source_events_total{provider,action,result}`
- `kubelease_source_errors_total{provider,action}`
- `kubelease_webhook_requests_total{action,result}`
