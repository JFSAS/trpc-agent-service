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
