FROM node:25-alpine AS frontend
WORKDIR /src
COPY package.json package-lock.json tsconfig.web.json ./
RUN npm ci --ignore-scripts
COPY web ./web
RUN npm run build

FROM golang:1.25.5-alpine AS build
WORKDIR /src
COPY go.mod go.sum VERSION version.go ./
COPY cmd/web ./cmd/web
COPY internal/web ./internal/web
COPY internal/avatar ./internal/avatar
COPY internal/educoder ./internal/educoder
COPY internal/problem ./internal/problem
ARG VCS_REF=unknown
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X educoder-cli.Commit=${VCS_REF}" -o /out/educoder-tools ./cmd/web

FROM alpine:3.22
RUN apk add --no-cache ca-certificates \
    && addgroup -S -g 10001 app \
    && adduser -S -D -H -u 10001 -G app app
WORKDIR /app
COPY --from=build /out/educoder-tools /usr/local/bin/educoder-tools
COPY web/index.html web/styles.css ./web/
COPY --from=frontend /src/web/dist ./web/dist
USER 10001:10001
ENV HTTP_ADDR=0.0.0.0:11454 \
    WEB_STATIC_DIR=/app/web
EXPOSE 11454
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -q -T 3 -O /dev/null http://127.0.0.1:11454/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/educoder-tools"]
