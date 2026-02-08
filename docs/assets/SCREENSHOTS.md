# Screenshot and visual assets

| File | Description | How to regenerate |
|---|---|---|
| `demo.svg` | Static CLI story (README hero) | Edit SVG or replace with `demo.gif` from VHS |
| `demo.gif` | Animated CLI demo (optional) | `vhs docs/demo/kubelease.tape` |
| `github-pr-flow.svg` | PR → webhook → lease → Namespace | Edit SVG |
| `social-preview.png` / `.svg` | GitHub social preview 1280×640 | Export SVG or update PNG |
| `architecture.svg` | Optional architecture diagram | Edit SVG / Mermaid in docs |

Prefer Mermaid or short terminal text over screenshots that go stale every release.
When CLI column layouts change, regenerate `demo.svg` / `demo.gif`.

Do **not** add fake Grafana dashboards.
