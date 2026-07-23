# Central monitoring

This directory contains the values for the central Prometheus, Grafana and
Alertmanager stack running on the `platform-mgmt` cluster.

Prometheus accepts low-volume remote-write traffic from the six local workload
clusters. Grafana credentials and future Slack webhook credentials are created
as Kubernetes Secrets and are never committed to Git.
