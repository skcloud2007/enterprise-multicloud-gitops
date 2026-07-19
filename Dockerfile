# syntax=docker/dockerfile:1.7
# Build on the runner's native architecture while cross-compiling the Go binary
# for BuildKit's requested target architecture. The scratch runtime needs no
# emulation because it contains only the target binary and CA certificates.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build

ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src

COPY go.mod ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
    -trimpath \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/multicloud-demo-app \
    ./cmd/server

FROM scratch

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/multicloud-demo-app /multicloud-demo-app

USER 10001:10001
EXPOSE 8080

ENTRYPOINT ["/multicloud-demo-app"]
