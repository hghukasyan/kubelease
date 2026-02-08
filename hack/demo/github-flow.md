# GitHub flow recording notes

1. Install webhook + Secret (see `docs/github-integration.md`).
2. Open a PR against a mapped repository.
3. Show: GitHub delivery → `EnvironmentLease` created → Namespace Ready.
4. Close/merge PR → lease expired → Namespace removed.
5. Cut to `docs/assets/github-pr-flow.svg` if live GitHub is unavailable.

Do not record real webhook secrets.
