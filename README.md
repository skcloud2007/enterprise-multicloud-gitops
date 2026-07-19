# Multi-Cloud GitOps — Development

This repository is the development environment's desired-state source.
Argo CD combines these values with the reusable chart from
`multicloud-helm-charts` and deploys one release to each development cluster.

| Values file | Argo CD destination | Namespace |
|---|---|---|
| `aws.yaml` | `eks-dev` | `dev-demo-app` |
| `azure.yaml` | `aks-dev` | `dev-demo-app` |
| `gcp.yaml` | `gke-dev` | `dev-demo-app` |

`common.yaml` contains settings shared across all development clusters. Each
cloud file contains only the cloud identity and its response message.

The application image uses the immutable commit tag `f52de391`. Future
pipelines should update this tag through a reviewed commit instead of changing
running clusters directly.
