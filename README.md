# Multi-Cloud Platform Bootstrap

This repository contains the declarative bootstrap configuration for the central
Argo CD management plane.

## Responsibilities

- Define separate Argo CD projects for development and production.
- Restrict each project to its matching EKS, AKS, and GKE clusters.
- Restrict Git sources to the approved environment and Helm repositories.
- Host ApplicationSets that fan out applications across the three clouds.

## Repository boundaries

| Repository | Responsibility |
| --- | --- |
| `multicloud-platform-bootstrap` | Argo CD projects and ApplicationSets |
| `multicloud-helm-charts` | Reusable, environment-neutral Helm charts |
| `multicloud-gitops-dev` | Development values and desired state |
| `multicloud-gitops-prod` | Production values and desired state |
| `multicloud-demo-app` | Application source and GitLab CI pipeline |

## Bootstrap order

1. Install the central Argo CD instance.
2. Register the six workload clusters.
3. Configure read-only GitLab repository credentials in Argo CD.
4. Apply the AppProjects in `argocd/projects/`.
5. Apply ApplicationSets from `argocd/applicationsets/`.

Secrets, kubeconfigs, private keys, and local environment files must never be
committed to this repository.
