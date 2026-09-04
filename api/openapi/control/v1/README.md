# Control OpenAPI v1

`openapi.yaml` describes the Control API management surface: Identity, Platform
Operator administration, Tenant membership, Agent authoring, and Runtime Profile.
Runtime Profile directly adjusts the current V1; it has no reference-only DTO or
old Schema/Digest compatibility stack.

## Runtime Profile: 11 management operations

All paths below use the prefix `/v1/tenants/{tenant_id}/runtime-profiles`.

| Method | Suffix | Request / response |
| --- | --- | --- |
| POST | empty | create metadata; `201` with profile + public draft |
| GET | empty | paginated Profile metadata |
| GET | `/{profile_id}` | Profile metadata |
| PATCH | `/{profile_id}` | name/description update |
| GET | `/{profile_id}/draft` | public ProfileRead with current credential states |
| PUT | `/{profile_id}/draft` | ProfileWrite and Idempotency-Key; DraftWriteResult |
| POST | `/{profile_id}/credentials/update` | explicit live credential update and Idempotency-Key |
| POST | `/{profile_id}/draft/validate` | `{expected_revision}`; Validation Report |
| POST | `/{profile_id}/revisions` | `{expected_revision}`; first publish `201`, replay `200` |
| GET | `/{profile_id}/revisions` | Revision Summary page |
| GET | `/{profile_id}/revisions/{revision_number}` | public ProfileRead with fixed config and current credential states |

Draft PUT accepts `expected_draft_revision`, `credential_protocol_version: v1`,
`config`, and `credentials`. The four config collections must be explicit; PUT
replaces configuration rather than overlaying or deep-merging it. Credential
purposes accept typed keep/replace/clear actions; missing actions keep only the
current association for a retained, unchanged purpose/destination.

Draft PUT and live POST require an Idempotency-Key of 1–128 visible ASCII
characters without spaces. Complete input is limited to 512 KiB and a single
credential value to 64 KiB. Unknown, duplicate, case-variant, null, malformed,
masked, or inappropriate action/value inputs are rejected without echoing secrets.

PUT returns only `profile_id`, `draft_revision`, and `updated_at`. Live POST returns
`credential_revision` and the fixed command-result `status`. Live target fields are
`profile_revision_number`, `category`, `resource_name`, `purpose_field`, and
`association_token`; the action is replace/clear with a positive independent
`expected_credential_revision`. Clients never submit internal CredentialIDs.

## Write, Canonical, and Read ownership

Public writes use independent DTOs. Internal Canonical Spec uses both
`schema_version=v1` and `credential_protocol_version=v1`, non-secret configuration,
and server-generated CredentialIDs. Current credential values are encrypted in
Profile-owned storage, not copied into Spec, Manifest, receipts, or logs.

Public Draft/Revision reads use `config` and optional `credential_states`; they
never include `spec`, internal IDs, values, ciphertext, or masked fragments of the
original value. Detail states may include configured/status/credential_revision
and conditional-write association tokens, but these are mutable projections.
`spec_digest` refers to the internal Canonical document, not this redacted view.

Publish returns `{revision: ProfileRead}` **without dynamic credential_states**.
Its idempotent result remains stable after live rotation or clearing. Revision
lists use `RuntimeProfileRevisionSummary`, omit Spec/config/states, and do not load
`spec_jsonb`. Full detail/publication reads first verify internal Canonical content
and stored Digest, then redact. The Canonical JSON Schema is not the public GET or
PUT DTO schema.

## Authorization, COW, and live updates

ACTIVE Tenant OWNER/MEMBER can read, edit ordinary configuration, validate, and
publish. Credential replace/clear, removal of credential-bearing resources, and
live updates require OWNER. Platform Operator status does not bypass membership.

Ordinary Draft replace is copy-on-write: new CredentialID, new initial version,
no change to published associations. Draft clear only unlinks the Draft. Explicit
live replace updates the active value behind the same ID; live clear terminates
that ID. Neither changes published Revision/Manifest content or Digest. Draft CAS,
Credential CAS, authorization rechecks, and request-MAC receipts are independent
and transactional.

Storage credentials accept a PostgreSQL URI with explicit sslmode
`disable`/`require`/`verify-full` and no other options. Only password is encrypted;
`destination` fixes host, port, database, username, and sslmode. A live password
replacement cannot change that destination.

## Deployment and runtime status

Profile publication stays static and Agent-independent. Deployment matches
AgentVersion requirements by exact category/name in a selected ProfileRevision,
checks compatibility, and fixes only required resources in Manifest. No extra
resource mapping table, capability search, or fuzzy lookup is used. V1 has no
Environment, environment_id, hidden default, overlay, or inherited configuration.

Profile consumer Application methods exist. The optional Profile-owned internal
adapter route `POST /internal/v1/runtime-profiles/credentials/resolve` is separate
from these 11 management operations and is not a public management GET. It requires
paired trusted workload authentication and an ExecutionAuthorizationVerifier.
Default bootstrap supplies no real execution-owner verifier and does not enable
that route. Real Run/Attempt authorization, internal deployment transport, and
Worker batch initialization remain follow-up integration; no default permission
is assumed.

Control-plane DTO, storage, crypto, and tests have been added; final pass/fail
results belong to the current verification report, not this API overview.
The tool kind/capability remain closed to `mcp_streamable_http`/`web.search`.
Future built-in/workspace tool protocols are not added by this credential change.

See the [credential contract](../../../../docs/architecture-next/control-api/runtime-profile-credentials.md)
for internal consumption, failure, and key-configuration boundaries.
Generated clients and server bindings belong under `/gen`, not this directory.
