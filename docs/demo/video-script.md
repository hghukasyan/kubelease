# Demo video script (45–90 seconds)

No long spoken introduction — show the product immediately.

| Time | On screen | Narration (optional, short) |
|---|---|---|
| 0–10s | Problem: abandoned preview Namespace still running | “Preview envs are easy to create and easy to forget.” |
| 10–25s | `make demo` or `kubectl kubelease create demo --ttl 30m` | “KubeLease gives them a lease.” |
| 25–40s | `kubectl get ns` / `kubectl kubelease get demo` | “A constrained Namespace appears.” |
| 40–55s | Delete managed ResourceQuota; show recreate | “Drift is reconciled.” |
| 55–70s | `kubectl kubelease expire demo` → Namespace gone | “Expire → ownership-verified cleanup.” |
| 70–90s | Diagram: PR → webhook → lease → multi-cluster | “Same lifecycle from GitHub or remote clusters.” |

Keep total under 90 seconds. Prefer terminal + one diagram over slides.
