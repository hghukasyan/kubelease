# Show HN draft

**Title:** Show HN: KubeLease – Ephemeral Kubernetes environments that clean themselves up

**Body:**

I built KubeLease, a Go Kubernetes Operator that models temporary preview/dev
environments as a first-class CR (`EnvironmentLease`) with TTL, max lifetime,
renewal, idle expiration, policies, GitHub PR webhooks, and multi-cluster placement.

Problem: CI and scripts create Namespaces easily; cleanup is often a best-effort
hook that fails silently. Leftover LoadBalancers/PVCs/namespaces add cost and noise.

Approach: desired state in the Kubernetes API, reconciler + finalizers, ownership
annotations before delete, sticky remote placement, and status Conditions for
failures instead of silent drift.

Repo: https://github.com/hghukasyan/kubelease

`make demo` spins Kind + controller + a sample lease.

I'd love feedback on the CRD shape, multi-cluster safety model, and what would
make this useful in your platform.
