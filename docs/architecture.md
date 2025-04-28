# Architecture

KubeLease separates **desired lifetime** (`EnvironmentLease`) from **environment
provisioning** (controller) and **external integrations** (webhook service).

```text
GitHub / CI
	 |
	 v
Integration Service
	 |
	 v
Kubernetes API
	 |
	 v
EnvironmentLease
	 |
	 v
KubeLease Controller
	 |
	 +--> Namespace
	 +--> ResourceQuota
	 +--> LimitRange
	 +--> NetworkPolicy
```

## Components

| Component | Responsibility |
|---|---|
| `EnvironmentLease` CR | Desired TTL, policyRef, idleTTL, owner |
| `EnvironmentLeasePolicy` | Defaults and hard limits (cluster-scoped) |
| Controller | Provision/reconcile/cleanup; never trusts caller quotas beyond policy |
| Integration service (`kubelease-webhook`) | Authenticated create/expire/touch; GitHub PR lifecycle |
| CLI (`kubectl kubelease`) | Operator UX for create/list/extend/touch/expire |

## Ownership model

- Namespaces are **not** OwnerReferenced (cluster-scoped child of cluster-scoped parent is awkward and unsafe for adoption).
- Ownership uses labels + `kubelease.io/lease-uid` annotation.
- In-namespace resources use OwnerReferences to the lease.
- Finalizer on the lease drives cleanup.

## Trust boundary

```text
webhook / GitHub
  -> create/update/delete EnvironmentLease only
controller
  -> manage Namespace and supporting resources
```

The webhook ServiceAccount must **not** delete arbitrary Namespaces.
