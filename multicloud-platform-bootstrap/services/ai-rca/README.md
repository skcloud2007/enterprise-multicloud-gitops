# AI RCA Service

The AI RCA service receives authenticated firing-alert webhooks from central
Alertmanager, queries central Prometheus for bounded evidence, asks a local
Ollama model for a schema-constrained hypothesis, and sends a separate Slack
Block Kit RCA card.

## Safety boundaries

- The service has no Kubernetes credentials and performs no cluster mutation.
- The model is instructed to use only supplied evidence.
- Every analysis is marked as AI-assisted and requires human validation.
- Alertmanager-to-service requests use a bearer token stored in a Kubernetes
  Secret.
- Slack and webhook credentials are never stored in Git.
- Duplicate alert groups are suppressed in memory for a configurable TTL.

## Local verification

```bash
docker run --rm \
  --volume "$PWD:/src" \
  --workdir /src \
  golang:1.26-alpine \
  sh -c 'gofmt -w cmd/ai-rca/*.go && go test ./...'
```

## Configuration

| Variable | Default | Description |
|---|---|---|
| `LISTEN_ADDRESS` | `:8080` | HTTP listen address |
| `OLLAMA_URL` | `http://host.docker.internal:11434` | Ollama API |
| `OLLAMA_MODEL` | `llama3.2:3b` | Local RCA model |
| `PROMETHEUS_URL` | central in-cluster service | Evidence source |
| `SLACK_WEBHOOK_URL` | required | Slack incoming webhook |
| `WEBHOOK_TOKEN` | required | Alertmanager bearer token |
| `OLLAMA_TIMEOUT` | `120s` | Maximum generation time |
| `DEDUPE_TTL` | `4h` | Duplicate suppression window |
| `QUEUE_SIZE` | `20` | In-memory webhook queue |

## Endpoints

- `GET /healthz`
- `GET /readyz`
- `POST /api/v1/alerts`
