# Recommended good first issues

Create these as GitHub issues with labels `good first issue` + area label.

1. **docs: add k3d installation example** (`documentation`)  
   Mirror Kind steps for k3d users in `docs/installation.md`.

2. **cli: add `--output wide` for list** (`area/cli`)  
   Extra columns (createdAt, effectiveExpiresAt, policyRef) without breaking default.

3. **docs: troubleshooting Condition catalog** (`documentation`)  
   Table of Condition types → meaning → fix, sourced from controller code.

4. **examples: kustomize overlay for 01-basic** (`documentation`)  
   Tiny kustomization under `examples/overlays/basic`.

5. **tests: placement selector with zero Ready targets** (`area/multicluster`)  
   Assert clear `NoMatchingCluster` status in envtest.

6. **metrics: document ServiceMonitor scrape snippet** (`documentation`)  
   Short copy-paste for Prometheus Operator users (no fake Grafana).
