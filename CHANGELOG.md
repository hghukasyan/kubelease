# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Because APIs are `v1alpha1`, releases remain **`v0.x`** until a stability commitment
for 1.0.

## [Unreleased]

### Added

- Public launch polish: README, branding, one-command `make demo`, docs IA,
  examples, issue/PR templates, CONTRIBUTING, SECURITY, CODE_OF_CONDUCT,
  launch copy, and demo asset sources.
- README hero with brand logo, modern layout, and fixed diagram arrows.

### Changed

- Go toolchain **1.25 → 1.26**; Kubernetes client modules **v0.32 → v0.36**;
  controller-runtime **v0.20 → v0.24**; CI actions and golangci-lint **v2.9**.

### Notes

- First tagged release will be **v0.1.0** when maintainers cut a `v*` tag via
  GoReleaser (CLI archives, container image, checksums).

## Release policy

| Version | Meaning |
|---|---|
| `v0.x.y` | Pre-1.0; CRD/`v1alpha1` fields may evolve with release notes |
| `v1.0.0` | Only when API stability is explicitly committed |

See [`.github/release-notes-template.md`](.github/release-notes-template.md).
