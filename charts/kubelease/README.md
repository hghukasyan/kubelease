# KubeLease Helm chart

Install CRDs first (Helm does not own CRD lifecycle here):

```bash
make install
helm upgrade --install kubelease charts/kubelease \
  --namespace kubelease-system --create-namespace \
  --set image.repository=ghcr.io/hghukasyan/kubelease \
  --set image.tag=<tag>
```

## Values highlights

- `controller.replicas` / `controller.leaderElection`
- Separate webhook ServiceAccount + RBAC (**no Namespace delete**)
- `webhook.ingress` for exposing `/v1/leases` and `/v1/github/hooks`
- Non-root `securityContext`, probes, resource requests/limits

Replace the generated webhook Secret tokens before production use.
