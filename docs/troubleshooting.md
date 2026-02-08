# Troubleshooting

Do not paste Secrets, kubeconfigs, or webhook tokens into issues.

## Lease stays Pending / Provisioning

**Symptom:** phase never becomes `Active`.

**Inspect:**

```bash
kubectl describe environmentlease <name>
kubectl get environmentlease <name> -o yaml
kubectl -n kubelease-system logs deploy/kubelease-controller-manager
```

**Likely causes:** controller not running; policy rejection; no matching
ClusterTarget; remote API unreachable.

**Resolution:** fix Condition message (policy limits, placement selector,
ClusterTarget Ready), ensure CRDs and RBAC are installed.

## NoMatchingCluster

**Symptom:** Condition / status indicates no cluster matched.

**Likely reason:** placement selector labels do not match any enabled Ready
`ClusterTarget`, or all matching targets are at capacity.

**Inspect:**

```bash
kubectl kubelease cluster list
kubectl get clustertargets -o wide
```

**Resolution:** adjust selector, enable a target, or raise `maxActiveLeases`.

## TargetClusterUnavailable

**Symptom:** selected cluster cannot be contacted.

**Inspect:** ClusterTarget status/conditions; Secret `secretRef`; network from
control plane to remote API.

**Resolution:** restore remote API / credentials. Sticky placement will not
silently jump to another cluster once a Namespace exists.

## Namespace stuck Terminating

**Symptom:** Namespace remains in `Terminating`.

**Likely reason:** finalizers on resources inside the Namespace (not KubeLease).

**Inspect:**

```bash
kubectl get ns <ns> -o yaml
kubectl api-resources --verbs=list --namespaced -o name | xargs -n1 kubectl get -n <ns>
```

**Resolution:** remove blocking finalizers/resources carefully; then lease cleanup can finish.

## Cleanup finalizer stuck

**Symptom:** EnvironmentLease deleting but Namespace remains; Cleanup Condition false.

**Inspect:** describe lease Events/Conditions (`OwnershipMismatch`, remote errors).

**Resolution:** fix ownership annotation mismatch or acknowledge force cleanup
only when you understand the risk (see multicluster-security docs). Never force
without verifying the Namespace is safe to delete.

## PolicyRejected

**Symptom:** create/update rejected or lease Failed with policy message.

**Likely reason:** TTL/quota/network settings exceed policy maxima.

**Resolution:** lower requested limits or update the policy (platform owners).

## Notification delivery failed

Outbound durable notification CRs are **not** part of the current release.
If you expected Slack/webhook fan-out from a NotificationDestination API, that
is roadmap — not a runtime bug.

Inbound source webhooks: check webhook logs and HMAC/token configuration.

## GitHub signature rejected

**Symptom:** HTTP 401/403 from `/v1/github/hooks`.

**Likely reason:** webhook secret mismatch or wrong payload.

**Inspect:** webhook Deployment Secret (`github-webhook-secret`); GitHub app/webhook settings.

**Resolution:** align secrets; do not log raw payloads containing tokens.

## Remote cluster authentication failed

**Symptom:** ClusterTarget not Ready; IdentityDrift; client errors in logs.

**Inspect:** Secret keys; kube-system UID identity sticky check
([multicluster-security](multicluster-security.md)).

**Resolution:** restore correct credentials; if identity drifted intentionally,
follow the documented acknowledgement path.

## Empty CLI list

```text
No active EnvironmentLeases found.

Create one with:
  kubectl kubelease create demo --ttl 30m
```

If you expected leases, check kubeconfig context (`--context`) and that CRDs exist.
