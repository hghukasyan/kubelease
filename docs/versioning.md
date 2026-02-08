# Versioning and releases

KubeLease follows [Semantic Versioning](https://semver.org/).

## Why v0.x?

Custom resources are `platform.kubelease.io/v1alpha1`. Fields and validation may
evolve. A `v1.0.0` tag should wait until API stability is an explicit project
commitment.

## What a release includes

GoReleaser (see `.goreleaser.yaml` and `.github/workflows/release.yml`) is
intended to publish:

- CLI archives for darwin/linux amd64/arm64
- `checksums.txt`
- Container image to GHCR

Cut releases by pushing a `v*` tag. Until the first tag exists, install from source.

## Changelog

See [CHANGELOG.md](../CHANGELOG.md) and [`.github/release-notes-template.md`](../.github/release-notes-template.md).
