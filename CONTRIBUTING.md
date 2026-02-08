# Contributing to KubeLease

Thanks for helping. Keep changes focused, tested, and honest about capabilities.

## Development prerequisites

- Go (see `go.mod`)
- Docker
- `kubectl`
- Kind (for smoke / demo)
- Make

## Fork / clone

```bash
git clone https://github.com/<you>/kubelease.git
cd kubelease
```

## Setup

```bash
make manifests generate
make test
make lint
make cli
```

## Kind workflow

```bash
make demo          # or: kind create cluster && make install && make run
make demo-clean
```

Manual:

```bash
kind create cluster --name kubelease-dev
make install
make run
# another terminal
make cli && export PATH="$PWD/bin:$PATH"
```

## Code style

- `gofmt` / `make fmt`
- `make lint` (golangci-lint)
- Prefer clear errors over generic `validation failed`
- Keep reconciles level-based and idempotent

## Adding CRD fields

1. Edit types under `api/v1alpha1/`
2. `make manifests generate`
3. Update samples/examples/docs
4. Add/adjust unit and envtest coverage

## Generated files

Do not hand-edit `zz_generated.*` or CRD YAMLs under `config/crd/bases/` —
regenerate with Make.

## Testing expectations

| Command | Scope |
|---|---|
| `make test-unit` | Fast package tests |
| `make test` | Unit + envtest |
| `make test-smoke` / `make demo` | Kind path |
| `./hack/kind-multicluster-e2e.sh` | Multi-cluster (manual / optional) |

## Commit style

Conventional, short subject:

```text
feat(cli): improve empty list message
fix(controller): ...
docs: ...
chore: ...
```

## Pull requests

Use the PR template. Include:

- What / why
- How tested
- API / docs impact

Small PRs are easier to review than large launches.

## Good first issues

Look for labels `good first issue` and `help wanted`. Suggested starters:

- docs: add k3d installation example
- cli: add `--output wide` mode
- docs: expand troubleshooting with real Condition text
- metrics: document scrape config snippet
- tests: extra placement selector scenarios
- examples: kustomize overlay for basic demo

Avoid labeling deep controller correctness work as good first issues.

## Code of conduct

See [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).
