# Channel V1 HTTP contracts

These closed JSON Schemas belong to Control's ChannelAccount/ChannelBinding
module. Gateway consumes snapshot, credential-resolution and observation wires;
it does not import Control's internal Domain or Application packages.

- `account-create/update/enabled`, `credential-update`, `binding-create/target/enabled`
  describe tenant management request bodies. Paths, OWNER access, request byte
  limits, Idempotency-Key and transactional CAS are enforced by adapters/use cases.
- `account-snapshot` is a complete, unpaginated metadata snapshot. Digest input is
  exactly `{scope_id,source_epoch,snapshot_revision,accounts}`, serialized with
  RFC 8785 JCS, then SHA-256 with a `sha256:` prefix. Accounts sort by provider
  then account_id; credentials sort by purpose. Cross-field identities, uniqueness,
  required configured credentials for enabled accounts and digest integrity are
  semantic checks beyond Schema.
- `credentials-resolve-request` requires an exact consumer-specific purpose set.
  `telegram_registration` receives both purposes from one connection revision.
  The response intentionally has no schema_version field, matching the reviewed
  protocol. Raw values exist only in this authenticated, noncacheable response.
- `observations` is `{schema_version:1,observations:[...]}`. Reason codes are closed;
  free-form provider errors and credential-bearing messages are rejected.
- `common` supplies internal references. No consumer should validate against the
  permissive root of `common` as if it were a request schema.

The Go helper rejects duplicate keys, unknown/case-varied fields and invalid UTF-8.
It returns redacted classifications, not Schema errors containing submitted values.
A Schema match does not authorize a request or prove provider readiness.

Fixtures contain explicit test-only credentials. `snapshot-shape.json` tests wire
shape only; its placeholder digest is not an executable authoritative snapshot.
Actual digest validation and identity binding are tested in the owning Domain.

## Telegram diagnostic preflight

Eight independent `preflight-*.schema.json` contracts describe create/created,
view, claim/grant, resolve/resolved, and complete. The public surface exposes two
Session operations; three diagnostic mTLS operations have no normal runtime
consumer bypass. `telegram_preflight` is a workload authorization kind, **not**
a stored credential purpose. Only the dedicated resolved response carries a
`telegram.bot_token` value; no public/claim/completion object contains it.

`PreflightConfigDigest` validates canonical static origin, scope/epoch, and
redacted invalid-origin representation before hashing the frozen JCS object.
It performs no DNS/network activity. The shared golden vectors and 67-origin
matrix align with Gateway. `ValidatePreflightChecks` verifies the eight ordered,
closed check facts, their code/status/details and remote-call prerequisites;
only the first six determine outcome. The final recovery and delivery items stay
UNKNOWN, so diagnostic PASS never asserts restoration or real message delivery.
`Validate`/`Decode` also enforce these preflight cross-field semantics, digest
consistency, redacted public origin and exact path identities.

JSON Schema 2020-12 `prefixItems` closes the ordered check tuple. `const: null`
represents null exactly; `items: {}` with min/maxItems=8 does not permit a ninth
item. Authorization, current version checks, claim fencing, server deadlines,
rate limits and transactional completion remain in the consuming Application.
