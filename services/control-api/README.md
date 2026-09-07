# Control API

Control API is the management backend for the Agent platform. It owns global
identity, platform administration, Tenant membership, and Agent authoring.
Runtime Profile V1 implements reusable resource authoring, deterministic validation,
immutable Profile Revision publication, and Profile-owned encrypted credentials.
Deployment V1 implements deterministic two-source compilation and atomic Control
Publication. ChannelAccount/ChannelBinding now provide an explicitly configured
management and Gateway-supply slice, including a route-only Outbox Relay. Control API is
not part of the message execution hot path. Profile-owned credential-check and
resolution ports and the optional internal route exist; current default bootstrap
leaves the value-resolution route disabled unless trusted workload authentication
and an ExecutionAuthorizationVerifier are injected together.

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
- `GET /v1/tenants/{tenant_id}/member-candidates`
- `GET|POST /v1/tenants/{tenant_id}/members`
- `DELETE /v1/tenants/{tenant_id}/members/{user_id}`
- V1 roles are intentionally limited to `OWNER` and `MEMBER`. Owners search
  active non-members and manage memberships; member listing, addition, and
  removal are OWNER-only. Invitations and ownership transfer are later slices.

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

### Deployment authoring and publication

- `POST|GET /v1/tenants/{tenant_id}/deployments`
- `GET|PATCH /v1/tenants/{tenant_id}/deployments/{deployment_id}`
- `POST /v1/tenants/{tenant_id}/deployments/{deployment_id}/validate`
- `POST|GET /v1/tenants/{tenant_id}/deployments/{deployment_id}/revisions`
- `GET /v1/tenants/{tenant_id}/deployments/{deployment_id}/revisions/{revision_number}`
- Tenant-scoped membership authorization, metadata CAS, Create and Publish
  receipt-first idempotency, exact two-source version selection, and stable
  diagnostics.
- Closed Deployment Input, internal RuntimeManifest, public credential-free
  RuntimeManifest View, and `RuntimeManifestPublished.v1` event contracts.
- Pure exact-name Compiler with declared/used/closure separation, fixed
  `storage.session` and optional `storage.memory` roles, platform Adapter/range
  checks, and per-node callable assignment.
- PostgreSQL atomically creates an immutable DeploymentRevision, RuntimeManifest,
  stable Receipt, and `PENDING` Outbox event before advancing Latest. Real
  PostgreSQL integration tests cover the publication lifecycle and fixed-source
  behavior.

The [Deployment V1 design](../../docs/architecture-next/control-api/deployment.md)
accepts a fixed AgentVersion and ProfileRevision, resolves requirements by exact
category and name, and compiles an immutable DeploymentRevision and minimal
RuntimeManifest. V1 has no Environment business object, hidden default Environment,
Overlay, or user-provided binding tables. Storage uses separate session/memory
runtime roles rather than AgentSpec Slots.

A fixed platform execution contract supplies supported Adapter versions and limits.
The implemented V1 Profile write DTO accepts credentials directly; Profile owns their
encrypted PostgreSQL storage, internal identity, and authorization. Public Profile
credential display gets configured/status from a separate dynamic query. Revision
and Deployment configuration reads return fixed redacted Views without values,
ciphertext, or internal credential IDs. Dynamic status is not part of immutable
Revisions, RuntimeManifests, or fixed publication Receipt responses.
The project has no stable release yet: revise the existing /v1 and schema_version=v1
contract directly when needed. Direct credential input and Profile-owned encrypted
storage are implemented in the current V1. Development data may be rebuilt; no dual
protocol, legacy reader, or history-preserving migration is required. Published
snapshots in the target runtime model remain immutable. No separate user-managed
Secret object is introduced.

Deployment uses ProfileCredentialChecker.CheckUsable for metadata-only checks. The
pure Compiler consumes fixed configuration and produces the credential uses closure;
dynamic checker results gate publication and populate Application Reports only,
never Compiler inputs, Canonical Content, or digests.
The later Worker slice must use RuntimeCredentialResolver.ResolveForAttempt and the
Profile Owner's authenticated internal batch endpoint to obtain values for the fixed
Manifest. It must not query Profile SQL or load Draft/latest configuration. New
Attempts needing credentials depend on this endpoint's availability; already-resolved
Attempts reuse their validated set in process memory without further Resolve calls.
An uncertain batch response, process loss, or lost lease ends the Attempt; recovery
uses a new AttemptID, not a historical credential pin. Credential values never enter
Manifest, Outbox, or events; internal references are projected out of public reads.
Per-node tool assignment remains enforced by the future Worker Adapter.

Draft keep retains an ID, replace allocates a new ID, and clear detaches only the
Draft. A separate explicit OWNER-only Profile action may replace/clear an already-used
credential with independent Credential CAS, addressed by ProfileRevision and resource
field rather than a caller-selected internal ID. It affects new Attempts of existing
Manifests, not their immutable digests. Live clear revokes an ID permanently; replace
does not revive it, and re-entry requires a new Draft ID and publication. Changing a
fixed destination or audience
requires a new credential ID and configuration revision.

Runtime Profile currently supports only the MCP `web.search` Tool configuration
protocol; built-in and command Tool protocols are later extensions.

The current implementation completes the Control Publication boundary: schema and
event fixtures, Compiler, Application, PostgreSQL store, eight HTTP routes, Bootstrap,
and real PostgreSQL integration. Deployment Manifest-publication Outbox rows remain
`PENDING`; their Relay/consumer path is not implemented. This is distinct from the
implemented Channel route-only Relay and Gateway route consumer. Multi-replica PlatformExecutionContract identity
is enforced by a release-pinned expected digest before database access or HTTP
startup. Channel account/binding management, route changes and Gateway Control integration
are implemented, with separately recorded real Telegram inbound-to-RunRequested acceptance.
The Gateway embeds Routing, Admission, Connection, and Delivery in one Go workload;
its Control source starts one Delivery Runner that owns Maintenance, while explicit
fixture mode keeps standalone maintenance. Its migrations are 0001–0010.
Worker execution, Manifest body retrieval, the ReplyIntent consumer, and the complete reply chain
remain follow-up integration. See the [product usability TODO](../../docs/architecture-next/control-api/product-usability-todo.md)
for planned UX changes; those entries are not implemented management endpoints.

### Telegram diagnostic preflight

The configured Channel module adds two public operations and three independent
mTLS operations, implemented against the canonical shared Channel schemas:

- `POST /v1/tenants/{tenant_id}/channel-accounts/{account_id}/preflights`
- `GET /v1/tenants/{tenant_id}/channel-accounts/{account_id}/preflights/{preflight_id}`
- `POST /internal/v1/channel-preflights:claim`
- `POST /internal/v1/channel-preflights/{preflight_id}/credentials:resolve`
- `POST /internal/v1/channel-preflights/{preflight_id}:complete`

A saved disabled Telegram account needs neither a Binding nor a Deployment.
Session/OWNER creation, member result reads, explicit `telegram_preflight`
workload authorization, exact BotToken resolution, database leases, immutable
redacted results and bounded maintenance are separate from normal runtime
credential authorization. No Account, Route, Observation or existing Outbox
writes occur. The additive `0002_channel_preflights.sql` migration preserves the
deployed baseline. See [runtime operations](CHANNEL_RUNTIME.md#5-telegram-只读预检)
and the [frozen contract](../../docs/architecture-next/control-api/telegram-preflight-v1.md).
Control integration tests do not stand in for Gateway/Telegram/Web acceptance.

## Configuration

| Variable | Required | Default | Purpose |
| --- | --- | --- | --- |
| `CONTROL_DATABASE_URL` | yes | — | PostgreSQL connection string |
| `CONTROL_PROFILE_CREDENTIAL_KEY` | yes | — | Externally generated base64-encoded 32-byte Profile encryption/MAC master key |
| `CONTROL_DEPLOYMENT_ALLOWED_ENDPOINT_HOSTS` | no | closed built-in V1 host set | Comma-separated exact lowercase hosts accepted by the Deployment Compiler |
| `CONTROL_DEPLOYMENT_EXPECTED_CONTRACT_DIGEST` | yes | — | Release-pinned `sha256:` digest shared by all replicas; startup fails before DB access if the effective platform contract differs |
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
Prepare the Deployment contract digest once per release with the final host
configuration and the exact binary being deployed:

```sh
CONTROL_DEPLOYMENT_ALLOWED_ENDPOINT_HOSTS='model.example,state.example' \
  go run ./services/control-api/cmd/control-api -print-deployment-contract-digest
```

The CLI reads only host configuration plus the frozen contract; it needs neither
a database nor a Profile credential key and prints a single digest. Persist that
output as `CONTROL_DEPLOYMENT_EXPECTED_CONTRACT_DIGEST` in shared release
configuration. Do not calculate and assign the expected value separately inside
each replica's startup command. `LoadConfig` requires the digest and validates its
format; `New` independently checks the effective digest before opening PostgreSQL,
running migrations, or constructing the HTTP server. Existing `/healthz` behavior
is unchanged; a mismatched replica fails startup rather than serving traffic.
Host or binary-contract changes require a newly prepared shared release value.

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
│   ├── deployment/         # match, compile, atomically publish fixed Manifest
│   ├── channelbinding/     # accounts, private credentials, exact bindings, route Relay
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

## ChannelAccount / ChannelBinding

The implemented public surface has 11 operations under tenant-scoped
`channel-accounts` and `channel-bindings`. It is registered only when the platform
supplies `CONTROL_CHANNEL_CONFIG_FILE`. A separate mutually authenticated TLS
listener serves snapshots, exact current credential sets, and observations.
The route-only Relay publishes committed immutable route events to JetStream;
Deployment Manifest events remain independent. See [Channel runtime](CHANNEL_RUNTIME.md)
and the referenced [public OpenAPI](../../api/openapi/control/v1/channel-public.yaml).
Real Telegram reception has separate [joint acceptance evidence](../../docs/architecture-next/control-api/channel-acceptance.md)
and [Gateway inbound evidence](../../docs/architecture-next/channel-gateway/telegram-real-inbound-20260906.md),
not inferred from build/test success. Telegram and the public Go WeCom connector remain
in-process Gateway dependencies, not separate connector deployments. Helm stays at
FINAL-INTEGRATION after all production workloads are complete.

### Telegram receive modes

The existing ChannelAccount create/PATCH operations now accept a closed
`config.receive_mode` (`long_polling` by default for new creates, or `webhook`).
Only disabled accounts change mode under account CAS. Polling needs BotToken;
WebhookSecret remains optional stable credential metadata until configured.
Migration `0003_telegram_receive_modes.sql` preserves old accounts as webhook
and rejects duplicate physical Telegram Bots across scopes atomically.

Preflight tasks pin mode/policy and effective configuration, preserve legacy
results, and apply the fixed polling N/A matrix. This Control slice neither
starts polling nor changes Telegram registration. See [Channel runtime](CHANNEL_RUNTIME.md)
and the [mode-aware preflight contract](../../docs/architecture-next/control-api/telegram-preflight-v1.md#12-双接收模式的实现补充)
for migration, workload, idempotency and validation details. Gateway/Web integration
and deployment acceptance are separate from these local Control tests.
