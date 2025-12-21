# Multi-cluster Kind E2E

Creates control + two target Kind clusters, installs KubeLease on control,
registers targets, places leases by region, checks drift recovery and expiry.

```bash
./hack/kind-multicluster-e2e.sh
```

Requirements: `kind`, `kubectl`, `docker`, Go toolchain.

This is heavier than `hack/kind-smoke.sh` (local-only). Run manually or on a
dedicated CI workflow.
