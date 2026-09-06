set shell := ["zsh", "-cu"]

default:
    @just --list

fmt:
    gofmt -w $(find services tests api platform -type f -name '*.go')

test:
    go test ./...

test-integration:
    bash scripts/test-control-integration.sh

openapi:
    go test ./api/... ./services/control-api/internal/agent/domain ./services/control-api/internal/runtimeprofile/domain ./services/control-api/internal/deployment/domain

test-race:
    go test -race ./services/control-api/internal/identity/... ./services/control-api/internal/tenant/... ./services/control-api/internal/admin/... ./services/control-api/internal/agent/... ./services/control-api/internal/runtimeprofile/... ./services/control-api/internal/deployment/... ./services/control-api/internal/bootstrap

vet:
    go vet ./services/... ./api/... ./platform/...

vuln:
    go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./services/control-api/...

build:
    mkdir -p bin
    CGO_ENABLED=0 go build -trimpath -o bin/control-api ./services/control-api/cmd/control-api

run:
    go run ./services/control-api/cmd/control-api

compose-config:
    docker compose -f deploy/compose/compose.yaml -f deploy/compose/compose.local.yaml config --quiet

compose-up: compose-config
    docker compose -f deploy/compose/compose.yaml -f deploy/compose/compose.local.yaml up --detach --build --wait

compose-down:
    docker compose -f deploy/compose/compose.yaml -f deploy/compose/compose.local.yaml down

web-install:
    cd web && npm install

web-test:
    cd web && npm test

web-lint:
    cd web && npm run lint

web-build:
    cd web && npm run build

web-dev:
    cd web && CONTROL_API_BASE="${CONTROL_API_BASE:-http://127.0.0.1:18081}" npm run dev -- --hostname "${WEB_HOST:-127.0.0.1}" --port "${WEB_PORT:-13001}"

web-start: web-build
    cd web && CONTROL_API_BASE="${CONTROL_API_BASE:-http://127.0.0.1:18081}" npm start -- --hostname "${WEB_HOST:-127.0.0.1}" --port "${WEB_PORT:-13001}"

# Gateway ingress vertical slice; requires a dedicated integration broker.
gateway-build:
    mkdir -p bin
    CGO_ENABLED=0 go build -trimpath -o bin/channel-gateway ./services/channel-gateway/cmd/channel-gateway

gateway-test:
    go test ./services/channel-gateway/... ./api/events/... ./gen/events/... ./platform/im/wecom/...

gateway-integration:
    test -n "${GATEWAY_TEST_DATABASE_URL:-}" && test -n "${GATEWAY_TEST_NATS_URL:-}" && test "${GATEWAY_TEST_ALLOW_NATS_RESET:-}" = 1
    go test -race -count=1 ./services/channel-gateway/...

nats-config:
    go run ./services/channel-gateway/cmd/channel-gateway nats-config deploy/nats/permissions.yaml > deploy/nats/server.conf

gateway-reconcile:
    go run ./services/channel-gateway/cmd/channel-gateway reconcile

# Public protocol library: local WebSocket fixtures, no PG/NATS/Bot credentials.
wecom-test:
    go test -race -count=1 ./platform/im/wecom/...
