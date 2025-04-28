# GitHub pull request integration

Endpoint: `POST /v1/github/hooks`

## Signature verification

GitHub `X-Hub-Signature-256` is verified with an HMAC secret stored only in a
Kubernetes Secret (`github-webhook-secret`). Secrets are never written onto
`EnvironmentLease` specs.

## Lifecycle

| Event | Behavior |
|---|---|
| `opened` | Ensure lease exists |
| `reopened` | Ensure/create active lease |
| `closed` | Expire lease (`SourceClosed`) |
| `closed` + `merged=true` | Expire lease |

```text
PR Opened → GitHub Webhook → Integration Service → EnvironmentLease → Namespace
PR Closed → expire lease → Controller cleanup → Namespace removed
```

## Deterministic identity

```text
my-company/payments-api PR #1842
  → EnvironmentLease/payments-api-pr-1842
```

Long repository names are truncated with a stable hash while keeping `-pr-<n>`.
Name collisions across orgs that share a repo name return HTTP 409.

## Metadata (annotations/labels)

Safe source metadata only: owner, repo, PR number, head SHA, branch, delivery ID.
No tokens.

## Policy mapping

`--github-repo-policies='{"my-company/payments-api":"preview-default"}'`

Duplicate deliveries (`X-GitHub-Delivery`) are idempotent.
