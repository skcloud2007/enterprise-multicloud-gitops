# Multi-Cloud Demo Application

A small Go HTTP service used to demonstrate the same application running on
EKS, AKS, and GKE-style Floci clusters through Helm and Argo CD.

## Endpoints

| Endpoint | Purpose |
|---|---|
| `/` | Returns the configured environment, cloud, message, and app version |
| `/healthz` | Liveness endpoint |
| `/readyz` | Readiness endpoint |
| `/metrics` | Minimal Prometheus-compatible application metrics |

## Configuration

| Variable | Default |
|---|---|
| `APP_ENVIRONMENT` | `local` |
| `APP_CLOUD` | `local` |
| `RESPONSE_MESSAGE` | `Hello from the multi-cloud demo application` |
| `PORT` | `8080` |

## Run locally with Go

```bash
go test ./...
go run ./cmd/server
```

## Run locally with Docker

```bash
docker build --build-arg VERSION=local -t multicloud-demo-app:local .
docker run --rm -p 18080:8080 \
  -e APP_ENVIRONMENT=dev \
  -e APP_CLOUD=aws \
  multicloud-demo-app:local
```

Then open <http://localhost:18080/>.

## Pipeline

GitLab CI runs formatting checks, `go vet`, unit tests, and then builds and
pushes an image to this project's GitLab Container Registry. Commit-SHA tags
are immutable deployment references; `latest` is also published from `main`
for convenience but is not intended for production GitOps manifests.
