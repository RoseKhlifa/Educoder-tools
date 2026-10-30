FROM golang:1.25.5-alpine AS build
WORKDIR /src
COPY go.mod go.sum VERSION version.go ./
COPY cmd/reuse ./cmd/reuse
COPY internal/compat ./internal/compat
COPY internal/educoder ./internal/educoder
ARG VCS_REF=unknown
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X educoder-cli.Commit=${VCS_REF}" -o /out/educoder-tools ./cmd/reuse

FROM alpine:3.22
RUN apk add --no-cache ca-certificates \
    && addgroup -S -g 10001 app \
    && adduser -S -D -H -u 10001 -G app app
WORKDIR /app
COPY --from=build /out/educoder-tools /usr/local/bin/educoder-tools
COPY resource-reuse/site ./resource-reuse/site
USER 10001:10001
ENV REUSE_ADDR=0.0.0.0:11454 \
    REUSE_STATIC_DIR=/app/resource-reuse/site
EXPOSE 11454
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -q -T 3 -O /dev/null http://127.0.0.1:11454/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/educoder-tools"]
