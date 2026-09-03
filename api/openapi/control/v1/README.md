# Control OpenAPI v1

[`openapi.yaml`](openapi.yaml) is the source contract for the implemented
Control API V1 routes:

- Identity login, current user, logout, and password change.
- Platform Operator capabilities, operator grants, global users, and Tenant
  provisioning.
- Current-user Tenant listing and Tenant member management.
- Tenant-scoped Agent creation, Draft editing/validation, and immutable Version
  publication/query.
- Tenant-scoped Runtime Profile creation, metadata updates, Draft
  editing/validation, and immutable Profile Revision publication/query.

The Runtime Profile surface contains the ten accepted V1 operations under
`/v1/tenants/{tenant_id}/runtime-profiles`. Draft payloads remain JSON objects
that may be incomplete, while every published `RuntimeProfileRevision.spec`
references the frozen
[`RuntimeProfileSpec` V1 schema](../../../schemas/runtimeprofile/v1/runtime-profile-spec.schema.json).
Publishing a source Draft revision for the first time returns `201`; an
idempotent retry returns the same immutable revision with `200`.
Revision list responses use `RuntimeProfileRevisionSummary` and omit the
potentially large `spec`; publish and single-revision responses continue to use
the complete `RuntimeProfileRevision`, including its canonical `spec`.

Generated clients and server bindings belong under `/gen`, not here.
