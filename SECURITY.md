# Security Policy

## Supported versions

KubeLease is pre-1.0 (`v0.x`). Security fixes target the latest published release
tag on `main` when releases exist; until then, report against current `main`.

The `v1alpha1` API may evolve before 1.0 — see release notes for breaking changes.

## Reporting a vulnerability

**Do not open a public GitHub issue for security vulnerabilities.**

Prefer [GitHub private vulnerability reporting](https://docs.github.com/en/code-security/security-advisories/guidance-on-reporting-and-writing/privately-reporting-a-security-vulnerability)
on this repository (Security → Report a vulnerability), if enabled by the
maintainers.

If private reporting is unavailable, contact the repository owner via a private
channel listed on their GitHub profile — do not include production secrets in email
subject lines or unencrypted attachments when avoidable.

## What to include

- KubeLease version / commit
- Kubernetes version
- Affected component (controller, webhook, CLI, Helm)
- Reproduction steps
- Impact assessment
- Whether a fix is already known

**Never** paste live tokens, kubeconfigs, private keys, or customer data.

## Scope notes

- Cleanup refuses Namespaces that fail ownership checks — unexpected deletes
  outside that model are high priority.
- Webhook HMAC/token bypasses are high priority.
- Multi-cluster credential handling and identity drift are high priority.
