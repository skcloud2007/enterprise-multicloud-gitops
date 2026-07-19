# ApplicationSets

The two ApplicationSets generate six Argo CD Applications from a compact list
of cloud and cluster mappings.

| ApplicationSet | Clusters | Sync policy |
|---|---|---|
| `multicloud-demo-dev` | `eks-dev`, `aks-dev`, `gke-dev` | Automated, prune, self-heal |
| `multicloud-demo-prod` | `eks-prod`, `aks-prod`, `gke-prod` | Manual approval |

Each generated Application uses two Git sources:

1. The reusable chart from `multicloud-helm-charts`.
2. Common and cloud-specific values from the matching environment repository.

Production intentionally has no automated sync block. A reviewed production
Git commit changes desired state, and an operator explicitly approves the Argo
CD sync.
