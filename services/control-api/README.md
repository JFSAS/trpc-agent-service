# Control API

Control API is the management backend for the Agent platform. It owns global
identity, platform administration, Tenant membership, and Agent authoring.
Runtime Profile V1 implements reusable resource authoring, deterministic validation,
immutable Profile Revision publication, and Profile-owned encrypted credentials.
Deployment and Channel Binding remain later management capabilities. Runtime routing
and configuration use immutable projections, not synchronous Control API configuration
reads. Profile CheckUsable, ResolveForAttempt, and an optional internal HTTP adapter
already exist; production Run/Attempt authorization and Worker wiring remain pending.
The default process bootstrap does not enable that internal route.

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

### Runtime Profile authoring and publication

- `POST|GET /v1/tenants/{tenant_id}/runtime-profiles`
- `GET|PATCH /v1/tenants/{tenant_id}/runtime-profiles/{profile_id}`
- `GET|PUT /v1/tenants/{tenant_id}/runtime-profiles/{profile_id}/draft`
- `POST /v1/tenants/{tenant_id}/runtime-profiles/{profile_id}/credentials/update`
- `POST /v1/tenants/{tenant_id}/runtime-profiles/{profile_id}/draft/validate`
- `POST|GET /v1/tenants/{tenant_id}/runtime-profiles/{profile_id}/revisions`
- `GET /v1/tenants/{tenant_id}/runtime-profiles/{profile_id}/revisions/{revision_number}`
- Tenant-scoped membership authorization, optimistic Draft revisions, deterministic
  RuntimeProfileSpec validation and canonicalization, delayed-retry-safe idempotent
  publication, and immutable ProfileRevision snapshots.
- Eleven management operations use independent Write, internal Canonical, and
  public Read DTOs. Credential values are write-only and encrypted in Profile-owned
  tables; public reads never expose Spec, internal IDs, values, or ciphertext.
- Draft credential changes use copy-on-write; explicit OWNER-only live updates use
  independent Credential CAS. Both write paths require Idempotency-Key and store
  private request-MAC receipts, not original inputs.
- Revision collection reads return metadata-only summaries without `spec_jsonb`;
  detail GETs verify the full internal Canonical Spec and Digest before projecting
  public config and current credential states. Publication responses omit dynamic
  states, so delayed idempotent publication remains stable.
- RuntimeProfileSpec V1 is a closed protocol with exactly one accepted Kind in each
  category: `openai_compatible`, `mcp_streamable_http`, `qdrant_openai`, and
  `postgres_state`.

The implementation contract is
[`api/openapi/control/v1/openapi.yaml`](../../api/openapi/control/v1/openapi.yaml).
The Runtime Profile ownership and protocol contracts are
[`runtime-profile.md`](../../docs/architecture-next/control-api/runtime-profile.md) and
[`runtime-profile-spec.md`](../../docs/architecture-next/control-api/runtime-profile-spec.md).
Runtime Profile V1 does not publish NATS events, generate a Deployment or
RuntimeManifest, or construct Worker/tRPC-Agent-Go runtime objects. Profile-owned
CheckUsable, ResolveForAttempt, and the optional runtimehttp adapter exist, but
production Run/Attempt authorization and Worker wiring remain follow-up tasks.
The internal route is registered only when trusted workload authentication and
an ExecutionAuthorizationVerifier are injected together; neither has a permissive
default, and current process bootstrap leaves the route disabled.

## Planned next slice: Deployment V1

The [Deployment V1 design](../../docs/architecture-next/control-api/deployment.md)
accepts a fixed AgentVersion and ProfileRevision, resolves requirements by exact
category and name, and compiles an immutable DeploymentRevision and minimal
RuntimeManifest. V1 has no Environment business object, hidden default Environment,
Overlay, or user-provided binding tables. Storage uses separate session/memory
runtime roles rather than AgentSpec Slots.

A fixed platform execution contract supplies supported Adapter versions and limits.
The implemented current V1 Profile write DTO accepts credentials directly; Profile owns their
encrypted PostgreSQL storage, internal identity, and authorization. Public Profile
reads combine non-secret config and current credential states without values, ciphertext,
or internal credential IDs. Deployment configuration reads will return a fixed redacted
View. Dynamic status is not part of immutable Revision content, RuntimeManifests, or
fixed publication Receipt responses.
The existing /v1 and schema_version=v1 contract has been directly updated without a
ref-only compatibility branch. Development data may be rebuilt explicitly; published
snapshots in the target runtime model remain immutable. Control Profile source, Schema,
fixtures, and encrypted storage are now aligned with origin/main at bf107766.
No separate user-managed Secret object is introduced.

Deployment uses ProfileCredentialChecker.CheckUsable for metadata-only checks. The
pure Compiler consumes fixed configuration and produces the credential uses closure;
dynamic checker results gate publication and populate Application Reports only,
never Compiler inputs, Canonical Content, or digests.
The planned Worker adapter will use RuntimeCredentialResolver.ResolveForAttempt and
the Profile Owner's authenticated internal batch endpoint for the fixed Manifest. It
does not query Profile SQL or load Draft/latest configuration. New Attempts needing
credentials depend on this endpoint's availability; already-resolved Attempts reuse
their validated set in process memory without further Resolve calls. An uncertain
batch response, process loss, or lost lease ends the Attempt; recovery uses a new
AttemptID, not a historical credential pin. Credential values never enter Manifest,
Outbox, or events; internal references are projected out of public
reads. Per-node tool assignment remains enforced.

Draft keep retains an ID, replace allocates a new ID, and clear detaches only the
Draft. The existing explicit OWNER-only Profile action replaces/clears an already-used
credential with independent Credential CAS, addressed by ProfileRevision and resource
field rather than a caller-selected internal ID. It affects new Attempts of existing
Manifests, not their immutable digests. Live clear revokes an ID permanently; replace
does not revive it, and re-entry requires a new Draft ID and publication. Changing a
fixed destination or audience
requires a new credential ID and configuration revision.

Runtime Profile currently supports only the MCP `web.search` Tool configuration
protocol; built-in and command Tool protocols are later extensions.

Profile credential protocol/Owner interfaces are implemented. Remaining Deployment
implementation order: protocol and fixtures → pure Compiler → Application →
PostgreSQL atomic publication/Outbox and HTTP → optional Relay. Channel Binding
activation and Gateway/Worker execution follow as separate slices. Deployment has
no implemented routes yet; planned routes are intentionally excluded from
**Implemented V1** above.

## Configuration

| Variable | Required | Default | Purpose |
| --- | --- | --- | --- |
| `CONTROL_DATABASE_URL` | yes | — | PostgreSQL connection string |
| `CONTROL_PROFILE_CREDENTIAL_KEY` | yes | — | Externally generated base64-encoded 32-byte Profile encryption/MAC master key |
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

The Profile credential key is also required when no Profile has been created yet.
There is no built-in key. All replicas and restarts using the same credential data
must reuse the same key; changing it without a separate key-rotation design breaks
existing encrypted values and conditional-write MACs. Preserve it independently
from database backups and never commit or log it.
See the [Compose guide](../../deploy/compose/README.md) for the local startup flow.

## Source layout

```text
services/control-api/
├── cmd/control-api/main.go
├── internal/
│   ├── identity/           # account, credential, session, authentication
│   ├── admin/              # Platform Operator and cross-domain admin commands
│   ├── tenant/             # Tenant and Membership rules
│   ├── agent/              # Agent, Draft, validation, immutable Version
│   ├── runtimeprofile/     # Profile, Draft, immutable Revision, private credentials
│   ├── deployment/         # design only: match, compile, publish fixed Manifest
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
