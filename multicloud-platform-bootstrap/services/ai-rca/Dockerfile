FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build

ARG TARGETOS
ARG TARGETARCH

WORKDIR /src

COPY go.mod ./
COPY cmd ./cmd

RUN CGO_ENABLED=0 \
    GOOS=$TARGETOS \
    GOARCH=$TARGETARCH \
    go build \
      -trimpath \
      -ldflags="-s -w" \
      -o /out/ai-rca \
      ./cmd/ai-rca

FROM gcr.io/distroless/static-debian12:nonroot

ARG VERSION=development

LABEL org.opencontainers.image.title="Multi-Cloud AI RCA Service" \
      org.opencontainers.image.description="Evidence-bounded local Ollama RCA service" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.source="https://gitlab.com/skcloud2007/multicloud-platform-bootstrap"

COPY --from=build /out/ai-rca /usr/local/bin/ai-rca

USER nonroot:nonroot

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/ai-rca"]
