# Enterprise Multi-Cloud GitOps

This private GitHub monorepo mirrors the source repositories used by the
enterprise multi-cloud GitLab delivery platform.

## Repository layout

- `multicloud-demo-app` — Go demonstration service and GitLab CI pipeline.
- `multicloud-helm-charts` — reusable application Helm charts.
- `multicloud-gitops-dev` — development environment desired state.
- `multicloud-gitops-prod` — production environment desired state.
- `multicloud-platform-bootstrap` — Argo CD projects, ApplicationSets, and UI configuration.

GitLab remains the primary CI/CD and GitOps source. This repository provides
a consolidated GitHub mirror.
