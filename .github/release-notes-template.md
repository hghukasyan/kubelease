# Release notes template

Copy into the GitHub Release body for each tag.

## Highlights

-

## Installation

### CLI

```bash
# download kubectl-kubelease_* archives from this release
# verify checksums.txt
```

### Container

```text
ghcr.io/hghukasyan/kubelease:<tag>
```

### Helm

```bash
make install   # CRDs
helm upgrade --install kubelease charts/kubelease \
  --namespace kubelease-system --create-namespace \
  --set image.repository=ghcr.io/hghukasyan/kubelease \
  --set image.tag=<tag>
```

## What's new

-

## Breaking changes

- (v1alpha1 may still change before 1.0)

## Fixes

-

## Contributors

-

## Container

-

## Helm chart

-

## Checksums

See `checksums.txt` attached to this release.
