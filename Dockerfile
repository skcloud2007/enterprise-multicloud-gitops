# syntax=docker/dockerfile:1.7
FROM golang:1.26-alpine AS build

ARG VERSION=dev
WORKDIR /src

COPY go.mod ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
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
