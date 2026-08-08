# Builds one image containing the API server and the UI5 dashboard it serves.
#
#   docker build -t kss-spp .
#   docker run -e SPP_DATABASE_URL=... -p 8080:8080 kss-spp
#
# Or use compose, which brings a PostgreSQL alongside it.

# ---------------------------------------------------------------------------
# 1. Build the UI5 app
# ---------------------------------------------------------------------------
FROM node:22-alpine AS webapp

WORKDIR /src

# Dependencies first, so a change to the app does not re-resolve the tree.
COPY package.json package-lock.json ./
RUN npm ci --no-audit --no-fund

COPY ui5.yaml ./
COPY webapp ./webapp

# --all bundles the OpenUI5 runtime into dist/resources, which index.html
# bootstraps from relatively. Without it the built app cannot start.
RUN npm run build

# ---------------------------------------------------------------------------
# 2. Build the API server
# ---------------------------------------------------------------------------
FROM golang:1.24-alpine AS backend

WORKDIR /src

COPY backend/go.mod backend/go.sum ./
RUN go mod download

COPY backend/ ./

# Static binary so the final stage needs no libc.
ENV CGO_ENABLED=0
RUN go build -trimpath -ldflags="-s -w" -o /out/spp-server ./cmd/server \
 && go build -trimpath -ldflags="-s -w" -o /out/spp-migrate ./cmd/migrate \
 && go build -trimpath -ldflags="-s -w" -o /out/spp-seed-demo ./cmd/seed-demo \
 && go build -trimpath -ldflags="-s -w" -o /out/spp-seed-users ./cmd/seed-users

# ---------------------------------------------------------------------------
# 3. Runtime
# ---------------------------------------------------------------------------
FROM alpine:3.20

# ca-certificates for outbound TLS; tzdata so the factory's timezone resolves.
RUN apk add --no-cache ca-certificates tzdata \
 && adduser -D -u 10001 spp

WORKDIR /app
COPY --from=backend /out/spp-server /out/spp-migrate /out/spp-seed-demo /out/spp-seed-users /usr/local/bin/
COPY --from=webapp  /src/dist /app/web

# The demo entrypoint, used by docker-compose.demo.yml. It is only reached when
# something asks for it; the default entrypoint below is still the plain server.
COPY scripts/demo-entrypoint.sh /usr/local/bin/spp-demo
RUN chmod +x /usr/local/bin/spp-demo

ENV SPP_ADDR=:8080 \
    SPP_WEB_DIR=/app/web \
    TZ=Asia/Phnom_Penh

USER spp
EXPOSE 8080

# The server exposes its own health endpoint; migrations run on start unless
# SPP_AUTO_MIGRATE=false.
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=5 \
    CMD wget -qO- http://127.0.0.1:8080/api/v1/health >/dev/null 2>&1 || exit 1

ENTRYPOINT ["spp-server"]
