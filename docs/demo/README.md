# Demo assets

## Terminal demo (VHS)

[`kubelease.tape`](kubelease.tape) regenerates a short GIF for the README.

### Prerequisites

1. [VHS](https://github.com/charmbracelet/vhs) installed
2. A cluster with KubeLease installed (`make demo`)
3. `kubectl-kubelease` on `PATH` (`make cli`)

### Record

```bash
# from repository root
vhs docs/demo/kubelease.tape
# writes docs/assets/demo.gif
```

Until a GIF is checked in, the README uses [`../assets/demo.svg`](../assets/demo.svg)
as a static stand-in with the same story.

## Scripts

| Path | Purpose |
|---|---|
| [`../../hack/demo.sh`](../../hack/demo.sh) | One-command Kind demo (`make demo`) |
| [`../../hack/demo/`](../../hack/demo/) | Scenario scripts for recordings |
| [`video-script.md`](video-script.md) | 45–90s video outline |

## Screenshots

Prefer regenerating from scripts rather than ad-hoc terminal capture.
See [`../assets/SCREENSHOTS.md`](../assets/SCREENSHOTS.md).
