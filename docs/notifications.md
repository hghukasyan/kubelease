# Notifications

## What exists today

- **Kubernetes Events** on `EnvironmentLease` for lifecycle and warnings
- **Prometheus metrics** (see [observability](observability.md))
- **Inbound** generic HTTP and GitHub webhooks that create/expire leases

## What is not implemented

Outbound durable notification delivery (destination CRs, Slack fan-out queues,
at-least-once webhook notifiers to chat systems) is **roadmap**, not available
API surface. Do not configure NotificationDestination resources expecting
behavior from this repository.

When outbound notifications land, this document will describe delivery
semantics, retries, and idempotency keys.
