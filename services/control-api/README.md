# Control API

Control API owns the management side of the Agent platform: tenants, Agent
authoring, reusable runtime profiles, deployments, runtime publication, and
channel bindings. It is not part of the message execution hot path.

## Current status

Only the compileable process and package skeleton exists. The remote-baseline
monolith and its root shell scripts have been removed. There are no HTTP routes,
database tables, NATS subjects, AgentSpec rules, or production-ready health
semantics yet.

## Source layout

```text
services/control-api/
├── cmd/control-api/main.go
├── internal/
│   ├── tenant/
│   ├── agent/
│   ├── runtimeprofile/
│   ├── deployment/
│   ├── channelbinding/
│   ├── infra/
│   │   ├── httpserver/
│   │   ├── postgres/
│   │   ├── nats/
│   │   └── telemetry/
│   └── bootstrap/
├── migrations/
└── Dockerfile
```

Each business module owns its `domain`, `application`, and concrete adapters.
Its `wiring.go` will expose one `NewModule` composition function. Shared
infrastructure provides connections and process mechanics, never business
repositories or domain policy.

Control API owns local username/password authentication and server-side
sessions as business capabilities. Their domain state, use cases, and concrete
adapters must live in an owning business module; authentication must not be
reintroduced as a shared `internal/infra/auth` package.

The governing rules are in
[`docs/architecture-next/constraints.md`](../../docs/architecture-next/constraints.md).
