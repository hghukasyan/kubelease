# Installation

## Options

| Method | When to use |
|---|---|
| `make demo` | Evaluate on Kind in one step |
| Kustomize (`make install` / `make deploy`) | Develop from source |
| Helm chart in `charts/kubelease` | Deploy controller + webhook with values |
| GitHub Release binaries | Install CLI after a `v*` tag is published |

There is no Homebrew formula yet. Do not document `brew install` until a tap exists.

## CRDs

Always install CRDs before the controller:

```bash
make install
# or
kubectl apply -f config/crd/bases/
```

## Controller (Kustomize)

```bash
make docker-build IMG=<registry>/kubelease:<tag>
# load or push the image for your cluster
cd config/manager && kustomize edit set image controller=<registry>/kubelease:<tag>
cd ../..
make deploy
```

Namespace: `kubelease-system`.

## Helm

The in-repo chart does **not** own CRD lifecycle. Install CRDs first:

```bash
make install
helm upgrade --install kubelease charts/kubelease \
  --namespace kubelease-system --create-namespace \
  --set image.repository=<registry>/kubelease \
  --set image.tag=<tag>
```

See [charts/kubelease/README.md](../charts/kubelease/README.md).

OCI chart publishing may land with the first release; until then use the path above.

## CLI

```bash
make cli
# binary: bin/kubectl-kubelease

# or
go install github.com/hghukasyan/kubelease/cmd/kubectl-kubelease@latest
```

After a release tag, prefer GitHub Release archives produced by GoReleaser
(`kubectl-kubelease_*_*.tar.gz` + `checksums.txt`).

## Uninstall

```bash
make undeploy
make uninstall
# demo Kind cluster:
make demo-clean
```

Expire or delete leases before removing CRDs so Namespaces can be cleaned up.
