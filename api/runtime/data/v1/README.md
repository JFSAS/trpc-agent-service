# Managed backend snapshot V1

Internal versioned wire contract shared by the future Deployment compiler and
Worker adapters. This package contains protocol shape, deterministic validation,
canonicalization and fixtures/tests, not business authorization or data access.

Four closed branches: PostgreSQL, Redis, Qdrant and S3. Each binds tenant identity,
backend ID/revision, a fixed adapter identifier, isolation contract, physical
connection target and finite timeout/concurrency/byte limits. Secrets have no
field. Only the selected target branch may exist. Decode rejects unknown and
case-variant fields, duplicate keys, null, missing required values and trailing
JSON. JSON Schema specifies structural validation; Go validation additionally
checks addresses, endpoint roots and reserved object bucket forms.

`Snapshot.Validate` is wire compatibility, **not proof that a Worker adapter is
implemented, running, authorized or online**. PlatformExecutionContract must still
explicitly admit the implementation before Deployment publication. Runtime must
verify the authenticated Tenant/Attempt against TenantID; the field does not
supply authority. Credential resolution belongs to the execution-side adapter,
keyed by the fixed backend identity/target, never by user-submitted file paths.

`Clone` detaches target pointers. Canonical Digest covers tenant, backend revision,
adapter, isolation, complete target and limits. Rotation of credentials does not
change this digest; target or isolation changes do. `Matches` rejects invalid
snapshots even when both happen to be zero values. Do not mutate caller-owned
snapshots or substitute current catalog values for frozen execution targets.

PostgreSQL/Redis Session descriptors require Tenant plus Session isolation.
Memory descriptors require Tenant plus subject plus stable Agent isolation.
Qdrant isolates Tenant/Profile/resource; S3 isolates Tenant/Artifact. These are
adapter obligations, not isolation automatically provided by this protocol.
Database and object-service ACL enforcement still requires actual runtime tests.

The first S3 runtime contract requires a general-purpose bucket with versioning
**disabled**, not suspended. This bounds deletion semantics for the initial
adapter; an implementation must verify deployment configuration before use.
Versioned buckets require explicit version cleanup support and are not silently
accepted. PostgreSQL/Redis TLS flags and HTTP endpoints are static configuration;
production transport policy is a separate deployment validation gate.

Current stage: protocol, private RuntimeCatalog resolver, and Profile selection
eligibility checks are implemented and tested. The P0b2 backend slice adds pure
Deployment compilation and persisted-read checks for selected managed Session
and Knowledge resources, plus redacted public backend identities. Memory and
Artifact request discovery follows explicit Agent enablement; complete capability
compilation remains guarded. The Artifact wire requires the Worker metadata
contract but does not yet mean Artifact compilation or execution is enabled.
Application/bootstrap catalog injection and production Worker adapter activation
remain pending. Profile checks without an injected BackendAccess reject managed
selections. Protocol/compiler tests do not prove an available online API or a
running four-backend deployment.

## Role-bound isolation (P0b1)

`Snapshot.ForRole` is called only after tenant/backend/revision/role authorization.
PostgreSQL and Redis support both `session` and `memory`; the latter uses
`tenant-subject-agent-v1`, not `tenant-session-v1`. Their shared physical target
need not be duplicated. Runtime consumers must use `ValidateForRole`: generic
`Validate` checks shape, not whether a Memory descriptor may open a Session.
Each role snapshot has its own authorization digest; changing role cannot reuse a
Session credential grant implicitly. This protocol does not implement RLS/ACL or
namespaces itself: Worker/platform provisioning must enforce them.
