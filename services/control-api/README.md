# Control API

Control API is the management backend for the Agent platform. It owns global
identity, platform administration, Tenant membership, and Agent authoring. Runtime
Profile, Deployment, and Channel Binding remain later management capabilities. It is
not part of the message execution hot path.

## Implemented V1

### Identity

- `POST /v1/auth/login`
- `POST /v1/auth/logout`
- `GET /v1/me`
- `POST /v1/me/change-password`
- Argon2id password hashes, opaque server-side sessions, forced first-login
  password rotation, and per-process login rate limiting.

### Platform administration

- `GET /v1/admin/capabilities`
- `GET|POST /v1/admin/operators`
- `DELETE /v1/admin/operators/{user_id}`
- `GET|POST /v1/admin/users`
- `GET|POST /v1/admin/tenants`
- Transactional initial Platform Operator bootstrap and last-operator
  protection.

### Tenant membership

- `GET /v1/me/tenants`
- `GET /v1/tenants/{tenant_id}`
- `GET|POST /v1/tenants/{tenant_id}/members`
- `DELETE /v1/tenants/{tenant_id}/members/{user_id}`
- V1 roles are intentionally limited to `OWNER` and `MEMBER`. Owners add
  existing active platform users; invitations and ownership transfer are later
  slices.

### Agent authoring and publication

- `POST|GET /v1/tenants/{tenant_id}/agents`
- `GET|PATCH /v1/tenants/{tenant_id}/agents/{agent_id}`
- `GET|PUT /v1/tenants/{tenant_id}/agents/{agent_id}/draft`
- `POST /v1/tenants/{tenant_id}/agents/{agent_id}/draft/validate`
- `POST|GET /v1/tenants/{tenant_id}/agents/{agent_id}/versions`
- `GET /v1/tenants/{tenant_id}/agents/{agent_id}/versions/{version_number}`
- Tenant-scoped membership authorization, optimistic Draft revisions, deterministic
  AgentSpec validation/canonicalization, idempotent publication, and immutable
  AgentVersion snapshots.

The implementation contract is
[`api/openapi/control/v1/openapi.yaml`](../../api/openapi/control/v1/openapi.yaml).

## Configuration

| Variable | Required | Default | Purpose |
| --- | --- | --- | --- |
| `CONTROL_DATABASE_URL` | yes | — | PostgreSQL connection string |
| `CONTROL_HTTP_ADDRESS` | no | `:8080` | HTTP listen address |
| `CONTROL_SESSION_LIFETIME` | no | `24h` | Fixed session lifetime |
| `CONTROL_SESSION_COOKIE_NAME` | no | `control_session` | Browser cookie name |
| `CONTROL_SESSION_COOKIE_DOMAIN` | no | empty | Optional cookie domain |
| `CONTROL_SESSION_COOKIE_SECURE` | no | `true` | Secure cookie flag |
| `CONTROL_BOOTSTRAP_MODE` | no | `disabled` | `auto` creates the first operator on an empty database |
| `CONTROL_BOOTSTRAP_USERNAME` | with `auto` | — | Initial operator username |
| `CONTROL_BOOTSTRAP_DISPLAY_NAME` | no | `Platform Operator` | Initial operator display name |
| `CONTROL_BOOTSTRAP_PASSWORD` | with `auto` | — | Initial operator temporary password |

Local plain-HTTP testing sets `CONTROL_SESSION_COOKIE_SECURE=false`. A new
database sets bootstrap mode to `auto` and supplies the username and temporary
password as process configuration. The first session is restricted until that
password is changed.

## Source layout

```text
services/control-api/
├── cmd/control-api/main.go
├── internal/
│   ├── identity/           # account, credential, session, authentication
│   ├── admin/              # Platform Operator and cross-domain admin commands
│   ├── tenant/             # Tenant and Membership rules
│   ├── agent/              # Agent, Draft, validation, immutable Version
│   ├── runtimeprofile/     # later vertical slice
│   ├── deployment/         # later vertical slice
│   ├── channelbinding/     # later vertical slice
│   ├── infra/              # shared process connections and mechanics
│   └── bootstrap/          # the single process composition root
├── integration/
├── migrations/0001_baseline.sql
└── Dockerfile
```

Business modules own `domain`, `application`, inbound/outbound adapters, and one
`wiring.go`. Business adapters use the uniform Go package names
`httpadapter` and `postgresadapter`. Shared infrastructure owns connections and
process mechanics, not repositories or business policy.

## Verification

```bash
just test
CONTROL_TEST_DATABASE_URL='postgres://...' just test-integration
just test-race
just vet
just vuln
just build
```
