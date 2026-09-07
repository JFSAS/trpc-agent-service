# Channel policy definition owner

This Control API module owns immutable Session/Quota policy **definitions**. It
is not a Worker Session store, quota consumption ledger, or access grant owner.
No new workload or Connector process is introduced.

- `domain`: closed session/quota definition shapes; bounded integer limits;
  JCS/SHA-256 revision integrity; strict decode and detached input ownership.
- `application.PublishedRevisionReader`: internal exact-reference read port.
- `adapter/outbound/postgres.Reader`: tenant/kind/id/revision lookup; revalidates
  the complete document and indexed digest, never substitutes latest revision.
- `channelbinding/adapter/outbound/policyowner`: translates owner records to the
  Channel publisher's Session/Quota ports without querying owner tables itself.

`application.Publisher` publishes immutable definitions with mandatory OWNER
access, expected-revision CAS and MAC-bound idempotency receipts. The PostgreSQL
publication adapter rechecks OWNER in the transaction, holds tenant/membership
locks and serializes each tenant/kind/id (including its first revision). A
revision, reference-only publication/audit outboxes and its compact receipt
commit together. A failed commit returns no success result; replay still requires
current OWNER access. Different keys do not bypass the revision fence.

`0007_channel_policy_definitions.sql` installs append-only revision and receipt
storage. Positive Session/Quota integration fixtures now call the real publisher,
then read exact owner documents through the Channel owner adapter before access
policy publication. Direct SQL document insertion remains only for corruption
injection. Tool isolation is still an explicit fixture.

The existing Control bootstrap now assembles this module when Channel is enabled,
using its real database, Session middleware, tenant OWNER reader/transaction
checks and configured request-MAC signer. No new workload is introduced.

`POST /v1/tenants/{tenant_id}/channel-policy-definitions/{kind}/{policy_id}/revisions`
publishes a session/quota definition. The required body contains
`expected_revision` (0 for first publish) and a closed `definition` branch;
the request requires `Idempotency-Key`. A successful request or replay returns
201 with the immutable reference, event IDs and `distribution=PENDING`.
OWNER checks precede body parsing. Authentication failures are non-cacheable.
This API manages reusable tenant-owned definitions, not account binding changes.

Definition listing and Web workflows, account policy authoring/binding, and the
definition event relay/schema remain pending. `distribution=PENDING` means an
outbox was committed, not that Gateway has applied the definition.

`enabled` is part of an immutable definition, not a global instant-revocation
signal. Current principal/access-policy revocation still needs the planned
runtime epochs, projection freshness and Worker/Gateway checks. Reader success
also does not prove runtime quota enforcement or tool isolation.

## Exact management reads

`GET /v1/tenants/{tenant_id}/channel-policy-definitions/{kind}/{policy_id}/revisions/{revision}`
is registered by the same module. It requires a current unrestricted Session and
tenant OWNER. A remembered definition reference grants no read authority.

The query service delegates to the exact PostgreSQL owner reader, then checks
returned tenant/kind/id/revision and full document integrity before returning the
immutable definition. It never substitutes the latest revision. Missing exact
versions return 404; cross-tenant or non-owner access is denied before storage.
Only canonical positive decimal revisions up to 9007199254740991 are accepted.
Query parameters, GET bodies and Content-Encoding are rejected. Responses,
including authentication failures, are no-store. Historical versions retain
their original definition and digest after new publications.
