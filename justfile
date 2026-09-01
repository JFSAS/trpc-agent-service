set shell := ["zsh", "-cu"]

default:
    @just --list

fmt:
    gofmt -w $(find services tests -type f -name '*.go')

test:
    go test ./...

test-integration:
    test -n "${CONTROL_TEST_DATABASE_URL:-}" || { echo 'CONTROL_TEST_DATABASE_URL is required'; exit 2; }
    go test -count=1 ./services/control-api/integration ./services/control-api/internal/bootstrap

openapi:
    go run github.com/getkin/kin-openapi/cmd/validate@v0.133.0 api/openapi/control/v1/openapi.yaml

test-race:
    go test -race ./services/control-api/internal/identity/... ./services/control-api/internal/tenant/... ./services/control-api/internal/admin/... ./services/control-api/internal/bootstrap

vet:
    go vet ./services/control-api/...

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
