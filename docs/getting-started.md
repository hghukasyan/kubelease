# Getting started

Assume you know Kubernetes basics but not KubeLease.

## Prerequisites

- `kubectl` configured against a cluster
- Go 1.25+ (to build CLI / controller from source)
- For the one-command path: Docker + [Kind](https://kind.sigs.k8s.io/)

## Fastest path

```bash
git clone https://github.com/hghukasyan/kubelease.git
cd kubelease
make demo
```

This creates a Kind cluster, installs CRDs and the controller, builds the CLI,
and creates an `EnvironmentLease` named `demo`.

## Install (existing cluster)

```bash
make install                 # CRDs
make docker-build IMG=kubelease:dev
# load image into your cluster (Kind: kind load docker-image kubelease:dev)
# then deploy (see docs/installation.md)
make cli
export PATH="$PWD/bin:$PATH"
```

Apply a policy (required for most real environments):

```bash
kubectl apply -f config/samples/platform_v1alpha1_environmentleasepolicy.yaml
```

## Create first lease

```bash
kubectl kubelease create demo --ttl 30m --max-ttl 2h --policy preview-default
```

## Inspect

```bash
kubectl kubelease list
kubectl kubelease get demo
kubectl describe environmentlease demo
kubectl get namespace "$(kubectl get envlease demo -o jsonpath='{.status.namespace}')"
```

## Renew / heartbeat

```bash
kubectl kubelease extend demo --for 15m
kubectl kubelease touch demo    # resets idle timer when idleTTL is set
```

## Expire

```bash
kubectl kubelease expire demo
# or: kubectl delete environmentlease demo
```

## Cleanup local demo cluster

```bash
make demo-clean
```

Next: [concepts](concepts.md) · [installation](installation.md) · [troubleshooting](troubleshooting.md)
