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
Kubernetes API (control cluster)
	 |
	 +--> EnvironmentLease / Policy / ClusterTarget
	 |
	 v
KubeLease Controller
	 |
	 +--> local cluster  OR  remote ClusterTarget
	         |
	         +--> Namespace / ResourceQuota / LimitRange / NetworkPolicy
```

## Components

| Component | Responsibility |
|---|---|
| `EnvironmentLease` CR | Desired TTL, policyRef, clusterRef, idleTTL, owner |
| `EnvironmentLeasePolicy` | Defaults and hard limits (cluster-scoped) |
| `ClusterTarget` CR | Remote cluster registration; credentials via Secret only |
| Controller | Provision/reconcile/cleanup on local or remote client |
| Integration service (`kubelease-webhook`) | Authenticated create/expire/touch; GitHub PR lifecycle |
| CLI (`kubectl kubelease`) | Operator UX for create/list/extend/touch/expire |

Omit `spec.clusterRef` to keep single-cluster behavior (local). See [multicluster.md](multicluster.md).

## Ownership model

- Namespaces are **not** OwnerReferenced (cluster-scoped child of cluster-scoped parent is awkward and unsafe for adoption).
- Ownership uses labels + `kubelease.io/lease-uid` annotation.
- In-namespace resources use OwnerReferences to the lease **only on the local cluster** (remote clusters lack the EnvironmentLease object).
- Finalizer on the lease drives cleanup; default remote cleanup is `RequireRemoteCleanup`.

## Trust boundary

```text
webhook / GitHub
  -> create/update/delete EnvironmentLease only
controller
  -> manage Namespace and supporting resources (local or via ClusterTarget kubeconfig)
  -> read credential Secrets referenced by ClusterTarget
```

The webhook ServiceAccount must **not** delete arbitrary Namespaces or read remote kubeconfig Secrets.
