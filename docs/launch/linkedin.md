# LinkedIn draft

Temporary Kubernetes environments are easy to create and hard to clean up —
especially when CI cleanup is a script that never runs.

I open-sourced KubeLease: a Go Operator that treats preview/dev environments as
an `EnvironmentLease` with TTL, renewal, idle expiry, platform policies, GitHub
PR lifecycle hooks, and multi-cluster placement.

Engineering focus areas:

- Idempotent reconciliation and finalizers
- Ownership-verified cleanup (refuse mismatched Namespaces)
- Sticky multi-cluster placement + remote identity checks
- Clear status Conditions instead of silent failure

Repo: https://github.com/hghukasyan/kubelease

`make demo` for a local Kind walkthrough. Feedback welcome.
