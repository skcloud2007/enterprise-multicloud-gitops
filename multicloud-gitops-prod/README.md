# Multi-Cloud GitOps — Production

This repository is the production environment's desired-state source. It is
separate from development so production promotion, review, and rollback can be
governed independently.

| Values file | Argo CD destination | Namespace |
|---|---|---|
| `aws.yaml` | `eks-prod` | `prod-demo-app` |
| `azure.yaml` | `aks-prod` | `prod-demo-app` |
| `gcp.yaml` | `gke-prod` | `prod-demo-app` |

`common.yaml` contains settings shared by all production clusters. Each cloud
file contains only the cloud identity and its response message.

The initial promotion uses immutable image tag `f52de391`, the same artifact
validated in development. Production changes should arrive through reviewed
commits or merge requests rather than direct cluster edits.
