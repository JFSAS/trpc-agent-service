# Worker admission-time authorization facts

The wire Adapter decodes the shared closed contract, preserves the complete fact
in an independent Worker domain value and binds it into event/logical Run digests.
Request JSON persistence retains actor, route and policy/principal versions. An
altered or stripped fact is not the same logical Run. Stored facts are observations,
not bearer capabilities and not a replacement for a current Worker projection.

Current behavior intentionally retains intake and Receipt replay but does not
allocate an Attempt for a request with authorization. Claim records one of the three wait reasons below without incrementing attempts. Migration 0005 enforces
the same hold at INSERT for old/direct database paths that ignore the new field.
Legacy requests remain on the existing V1 path. The hold is transitional, not the
finished F01 implementation: replace it with a locked, fresh independent policy /
principal / epoch check, then implement tool and approval checks. Production
Gateway gate/emission stays disabled until those dependencies and rollout gates
are complete. An old broker consumer may still reject the new closed field;
upgrading SQL alone does not make a mixed consumer fleet ready.

## Independent local projection (next increment)

Worker migration 0006 adds its own complete current snapshot and principal set.
`NewAuthorizationProjection` selects the shared installer with the Worker table
family and a Worker-owned pool. This avoids duplicating integrity/expiry logic;
it does not borrow Gateway database facts. An independent authenticated Control
reader/lifecycle is available as opt-in bootstrap configuration (see below).

Claim now locks and checks this local projection. It verifies current identity,
source epoch, generation/revision floors, immutable policy digest, principal state,
operation/group scope and DB-time freshness. A newer local REVOKED principal
supersedes the historical Admission ALLOW observation. Current state unknown is
`AUTHORIZATION_NOT_READY`; known denial is `AUTHORIZATION_DENIED`; passing this
local subject/policy check is `AUTHORIZATION_DEPENDENCIES_NOT_READY`, because
SessionPolicy/TenantQuota and execution capability checks are still pending.
All three remain waiting states with zero new Attempts. Migration 0005's database
hold remains intact; it must be replaced only with the completed execution fence.

## Optional independent source lifecycle

Worker config may include:

```json
"authorization": {
  "url": "https://control-channel-internal:8443",
  "scope_id": "pool",
  "source_epoch": "11111111-1111-4111-8111-111111111111"
}
```

Omission disables acquisition. The reader uses the existing `control_tls` client
certificate/private CA, but requires its own Control mapping with the exclusive
`worker_channel_authorization` capability. The explicit URL may differ from the
manifest API listener. This configuration does not enable authorized Attempts.

The bounded directory discovers targets from nonterminal persisted Worker Runs,
filtered by pinned scope and grouped by tenant/account/provider. Migration 0007
indexes that query. Target hints are not grants: Control ownership, source epoch,
complete page/policy proof and local transaction checks remain mandatory. The
shared scheduler polls, refreshes and backs off with bounded concurrency, and
joins Worker shutdown before its database pool closes.

Tests cover real Worker migrations and Ledger.Accept persistence, target filtering,
deduplication, configuration rejection, cancellation and idempotent Close. A
separate actual Control/PostgreSQL mTLS test verifies the public reader and revocation.
These are component integration proofs, not a single deployed App/real IM chain.

## Locked dependency document checks

Claim now reads both dependency documents under the same current-head lock as
policy/principal checks. The shared checker independently revalidates full content
and exact references, then requires enabled SessionPolicy and Quota. Missing or
corrupt content returns AUTHORIZATION_NOT_READY; known disabled content returns
AUTHORIZATION_DENIED. This supersedes the earlier subject-only local check.

Passing document checks still records AUTHORIZATION_DEPENDENCIES_NOT_READY and
zero Attempts: actual session partition application, shared quota reservations and
execution/tool/approval gates are not proved merely by reading their definitions.
No per-message synchronous Control request is added.

Pending authorized intake is an unassigned durable input, not an accepted Run.
StageAuthorization freezes its first request/policy/deadline; active pending rows
are refresh targets and count toward queue capacity, while all pending rows count
toward retained capacity. Ordinary Accept replays existing receipts first, then
returns ErrNotReady for any pending Event/Run/Admission identity; it cannot promote
or acknowledge that input.

Production bootstrap now supplies NewIntake to the application's narrow IntakeLedger
port. It replays historical receipts/accepted runs first, and durably stages each
new authorized input within the same identity-locked transaction before returning
ErrNotReady. The NATS consumer delays NAK rather than ACKing pending input. New
inputs do not select the old unpartitioned Session hash. Refresh discovery reads
these persisted inputs before any Session or Run exists. Explicit source/epoch
configuration is still required to run the authorization refresher; staging itself
is not a current grant and does not synchronously call Control.

Transactionally coupled registry/Run promotion, pending conflict/expiry terminal
results and retention cleanup remain pending. Until promotion is implemented,
authorized new inputs remain retryable and unaccepted; this is not an operational
end-to-end authorization release. Previously accepted facts continue their existing
replay contract, and non-authorization wire behavior is unchanged.


The pending Session route adapter now independently anchors caller-supplied scope
and actor to the persisted ingress input, enforces pinned scope/epoch and current
mapping revision floors, and asks a mandatory owner-supplied verifier to validate
the exact immutable runtime target in the same transaction. Session policy grants
remain checked by CurrentAuthorizer; pending message provenance never authorizes a
reset operation. Production atomic promotion is still not wired.


Policy-bound Run writer (not production-enabled): PendingRouteAuthority now exposes
WriteRunInTransaction and ConsumeRunInTransaction. The writer requires a non-nil
transactional reservation owner, checks the exact current SessionPolicy/partition
and locked registry generation, loads the ORIGINAL pending policy/deadline, and
writes real Session/Run/Receipt plus an immutable historical registry link. It does
not select the old request hash. A pending input's existing capacity slot is
replaced, not counted twice. Timestamp equality uses PostgreSQL microsecond precision
while the original request JSON preserves received_at precision.

The Registry owner performs final proof before ConsumeRunInTransaction validates
real Receipt/Run/link identities and removes pending. Migration 0011's deferred
constraint rejects committing a partition-linked new Run while its pending source
(or a same-Run/Admission pending alias) remains. Failed reservations/writes roll back
through savepoints; failed outer commits leave the original pending intact.

Tests use a TRANSACTION FIXTURE for reservation participation, not a real budget
implementation. Production has no reservation implementation or promoter wiring yet.
Authorized Claims remain zero-Attempt held; this writer is not an execution grant.
