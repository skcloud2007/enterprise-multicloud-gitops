# Multi-Cloud Helm Charts

This repository contains reusable, environment-neutral Helm charts. Environment
and cluster-specific values belong in the separate development and production
GitOps repositories.

## Charts

| Chart | Purpose |
| --- | --- |
| `multicloud-demo-app` | Secure deployment of the demonstration API |

## Validation

```bash
helm lint charts/multicloud-demo-app
helm template demo charts/multicloud-demo-app --namespace dev-demo-app
```

Chart releases use semantic versioning. Production GitOps configuration should
pin a chart release instead of following an unbounded branch.
