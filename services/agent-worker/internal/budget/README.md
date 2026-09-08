# Worker budget owner: Run quotas and operation consumption

This module implements two separate shared-ledger dimensions: published Run-count
quotas (`max_concurrent_runs`, `max_runs_per_minute`) and operation consumption
(`model_tokens`, `tool_units`). Neither is a provider pricing/billing service.
The operation ledger requires mandatory owner ports for current policy plus a
proved enforceable operation bound, and for final usage evidence. Those production
policy/usage readers and runtime Before/After filters are not wired yet; tests use
explicit owner fixtures. The public Quota definition still carries Run counts only.

A production Run reservation must compose all required dimensions; this module does
not yet implement the full `PendingRunReservation` port. No adapter substitutes
Run counts or unverified caller limits for bounded model/tool consumption.

## Reservation contract

- Input is stable tenant/Run/input-digest identity plus the complete Quota document.
  The public closed decoder verifies its complete content digest and tenant/kind.
  The caller separately proves the reference is currently authorized for this input;
  this immutable document or a replayed reservation is not a freshness grant.
- The first successful reservation fixes policy ID/revision/digest and DB timestamp.
  Matching retries replay before tenant/storage capacity checks. Changed input,
  tenant or policy under the same globally unique RunID conflicts; it cannot charge
  twice or silently reinterpret the reservation. Outer receipt-first intake normally
  replays accepted Runs before invoking any new reservation.
- Tenant-wide counts span policy IDs/revisions, so switching documents cannot reset
  quota. Zero means zero capacity, not unlimited. Disabled documents deny new holds.
- Lock order is stable RunID, tenant, global retained-storage bound. All counters and
  timestamps are PostgreSQL facts; there is no local counter or network I/O in tx.
- The rolling minute counts reservations newer than database time minus 60 seconds.
  Concurrent holds do not expire or refund due to age, timeout or unknown completion.
- `ReserveRunQuotaInTransaction` uses a savepoint but leaves commit to the intake
  owner. Failed/rolled-back Run intake must roll back quota in the same transaction.
  `ReserveRunQuota` is the owner-transaction wrapper, not the production intake path.

## Verified terminal release and incomplete lifecycle

`RunQuotaSettler` now composes a mandatory Execution terminal reader in the same
transaction. It releases concurrent occupancy only after committed Execution facts
prove either `NO_ATTEMPT` (system termination without any attempt) or
`SUCCEEDED_ATTEMPT` (one ended successful attempt, completion and session commit).
A terminal status alone, failed/aborted or ambiguous attempts, timeout and age do
not prove release. Migration 0013 records one immutable settlement per reservation.
Reservation replay never reactivates it. Minute-window charges and retained-history
counts are not refunded; settlement is not model usage/cost reconciliation.

Lock order is budget Run identity -> budget tenant -> Execution Session -> Run.
Call settlement after completion commits, not from a completion transaction that
already holds Execution locks. The owner-transaction API leaves commit to its caller.
Worker bootstrap now composes the real quota settler with the Execution Ledger
and launches a bounded reconciliation loop in its managed work lifetime. This
wiring only releases existing proved reservations; it creates no new reservations
and does not enable authorized execution. Retained-history/tombstone retention
and the production admission/cost-reservation promoter remain unimplemented.

Likewise, the production promoter still requires actual maximum model/tool cost
reservation, usage reconciliation and current authorization gates. This module does
not remove the existing zero-Attempt hold or enable PUBLIC_LIMITED execution.

## Evidence

Real Worker migrator/runtime roles and two independent DB pools race eight distinct
Run IDs against concurrency 1: one commits, seven receive quota denial. Tests cover
immutable replay, identity conflict, tenant isolation, policy-switch non-reset,
zero/disabled/corrupt definitions, rolling-minute limits, global retained bound and
outer rollback. Historical timestamps are explicit fixture inserts (not a real
minute-long wait); they prove rate-window exclusion and no age-based hold refund.
These are PostgreSQL component proofs, not deployed Worker or live IM acceptance.

Terminal tests use real Ledger acceptance, claim, completion and system termination,
with synthetic candidate content rather than an external model/storage call. They
cover active/unknown/status-only holds, rollback visibility, eight concurrent
idempotent settlements, immutable records, reservation replay, concurrency release
and preservation of the per-minute charge.

## Background reconciliation

- Each tick reads at most `limits.scan_batch` unsettled reservation identities
  (1..1000) through a keyset cursor, closes the row cursor, then settles each using
  a separate owner transaction. Worker `timing.operation_timeout` bounds the tick;
  `timing.poll_interval` applies after every tick, including errors and full pages.
- A known-not-terminal or erroneous early row advances the scheduling cursor;
  later rows still run. Empty tail wraps to the beginning, finding newly completed
  work and inserts below the previous cursor. A process restart can restart at the
  beginning: correctness depends on immutable DB facts, not the local cursor.
- Multiple Workers may inspect the same candidate; stable reservation locks and
  immutable settlement identity prevent double release. Unknown usage never
  becomes a refund merely because the scan repeats or reaches a deadline.
- The loop inherits shutdown cancellation and joins the Worker work waitgroup.
  Telemetry uses the bounded `quota_reconcile` operation and `quota_wait` outcome;
  routine unknown holds update metrics without flooding logs. Dependency errors
  remain failures. Release delays conservatively keep occupancy, not grant budget.
- PostgreSQL tests verify page bounds, fault/unknown traversal, wrap/late inserts,
  two-pool concurrent settlement, restart and cancellation. A separate test invokes
  actual bootstrap composition and loop against real Execution/Budget owners,
  observes a terminalized Run release, then reserves a new Run at concurrency 1.
  This is not a full NATS/HTTP App startup or external model/IM test.

## Operation-consumption ledger

- Identity binds tenant, Run, stable operation ID, input digest, unit, maximum and
  bound-proof digest. A physical retry/new external call needs a new operation ID.
  An identical reservation retry returns the original grant and DB timestamp before
  recharging or capacity checks. This receipt is not fresh permission to call a model.
- `ConsumptionAuthority` must verify current policy and the enforceable bound from
  real owner facts in the transaction. Nil ports and substituted fingerprints fail.
  The owner may return literal cap zero or disabled, both of which deny new holds.
  Test grants are not published Control policy or real SDK bound enforcement.
- Cap is cumulative tenant/unit consumption across policy IDs/revisions. Switching
  policy identity does not reset usage. No implicit daily window, epoch reset or
  currency conversion is invented. Token and tool units are distinct dimensions.
- Operation, tenant/unit and global-retained locks serialize reservations across
  instances. Unknown operations charge their maximum; known final ones charge
  actual usage. SQL NUMERIC aggregation avoids int64 accumulator overflow. The
  reservation/settlement records are append-only and storage has a configured bound.
- `ConsumptionReader` must bind final usage to the exact operation fingerprint and
  evidence identity/digest. Missing, non-final, forged or failed reads retain the
  maximum. Zero is accepted only with final proof. Tenant-scoped evidence IDs cannot
  settle two operations. No Run timeout or terminal status is treated as usage.
- Usage greater than the proved maximum is recorded, not rejected and lost during
  rollback. The generated `bound_violated` fact blocks subsequent reservations for
  that tenant/unit even if some nominal cap remains. It does not claim hard budget
  enforcement already succeeded. Explicit operator reconciliation/recovery and
  pricing-aware monetary budgets remain future work; no silent unfreeze exists.
- Both reserve/settle support owner transactions with savepoints. Production callers
  must compose them with durable operation dispatch/usage facts, follow the lock
  order (operation -> tenant/unit -> retained capacity -> authority, or operation ->
  tenant/unit -> usage owner), and avoid external I/O inside those transactions.
  The existing Run-quota reconciler does not manufacture consumption proofs.

Real PostgreSQL evidence uses two pools and eight distinct operations competing for
100 units at maximum60: one succeeds, seven are denied. Final usage20 then admits
an 80-unit reservation, while an extra unit is denied. Tests also cover unchanged
replay, identity/unit/tenant isolation, savepoint/outer rollback, unknown/faulty
proofs, disabled/zero caps, retained capacity, immutable facts, evidence reuse and
observed-overrun preservation/fencing. These generic ledger tests use fixture
authority/usage sources; the separate real Execution linkage test below replaces
the usage source and reservation verifier, but not the policy/bound authority.

## Actual SDK usage provenance

Worker's pinned OpenAI-compatible SDK sums usage across chunks and suppresses
empty-choice chunks; its final event also omits explicit zero usage. Execution
now observes unchanged HTTP/SSE bytes through a bounded streaming reader before
SDK normalization. A single completed stream must contain a same-response report after a supported
finish marker and end with DONE. The report must be complete, nonnegative,
consistent final report. Identical cumulative reports are deduplicated; conflicting,
missing, invalid, oversized or incomplete-stream evidence stays unknown. Explicit
zero is distinguishable from absent usage. This observer never changes response
bytes, retries the call or logs payloads; per-event buffers are bounded at 64 KiB.

`UsageKnown` propagates through the real runtime adapter to the Processor. Unknown
counters never produce a successful usage observation, while verified zero does.
The SDK's accepted-session snapshot is not the accounting authority. This supplies
actual reported usage provenance, not currency-priced billing. The following
sections describe durable facts and the real consumption reader. Production
policy/bound authority and dispatch integration remain required before enabling
model/tool reservation.

## Durable model usage before Session Stage

Execution now persists known model usage in its own `execution_model_usage` table
(migration 0015) before Processor stages the session candidate. Its deterministic
single-LLM operation ID binds tenant/Run/Attempt; the fact digest also binds Run
input, immutable Manifest, parent head and token counts. A new Attempt gets a new
operation ID. This is not a permission to repeat a physical model request.

New facts require the authenticated live EXECUTING grant. Exact receipt replay
checks the original Attempt capability and identity even after terminalization;
changed usage conflicts and neither overwrites facts nor extends their timestamp.
Unknown/default-zero reports are not persisted as final usage. Authenticated known
zero is valid. Usage survives subsequent Stage/Completion failure. A persistence
failure prevents Stage/Completion instead of silently acknowledging the result.
Only token counts, stable identities, digest and timestamp are stored, not model
content, raw provider responses or credentials.

Processor uses the real Ledger method in production composition. Tests separately
cover Processor ordering/failure behavior and real PostgreSQL grant authentication,
eight concurrent writes, immutable replay and post-model failure survival. They do
not yet prove full SDK+database+Control integration. An Execution usage fact alone
is not a current policy grant or maximum-cost proof; settlement requires the exact
pre-call reservation join described below.

## Exact pre-call reservation linkage and real usage settlement

Execution migration 0016 stores an immutable link from the single-LLM operation to
its Budget reservation fingerprint, input digest, bound digest and maximum.
`BindModelReservation` calls Budget's real `VerifyUnsettledReservation` owner port
before acquiring Execution locks. The caller composes reserve and bind in the same
transaction while the authenticated Attempt is PREPARING, commits, and only then
starts external I/O. New bindings after EXECUTING are rejected. Exact link replay
is a receipt, not authority to repeat a model call. Settled reservations cannot be
rebound. Neither owner queries the other owner's tables.

Execution Ledger now implements the real `ConsumptionReader`: it joins only its
own immutable link, usage and Attempt records, checks the exact request fingerprint
and recomputes the usage digest against actual Run/Manifest/parent identities.
Unknown or unlinked usage retains the maximum, including unlinked known zero.
Known linked usage remains chargeable after a later Session Stage failure.

The two-pool PostgreSQL test uses actual reservation verification and actual usage
reading: reserve60, bind atomically, persist usage20, fail Stage, settle20, then
reserve80 under cap100. It also checks outer rollback of both owners, replay,
substituted input/bound/maximum/unit rejection, late binding and immutable links.
Current policy and enforceable maximum authority still use a fixture. Production
Before-model dispatch and PendingRun admission are not enabled by this adapter
alone. Managed settlement recovery is now composed separately as described below.

## Managed consumption settlement recovery

`ConsumptionSettler` requires only the shared pool and final usage reader. It
exposes settlement/reconciliation, not reservation authority. The reservation
service embeds this accounting component but still requires real current-policy
and maximum-bound authorization for new holds. Bootstrap constructs the recovery
component with the real Execution Ledger; no permissive authority is supplied.

Worker starts separate Run-count and consumption scans on its managed work context.
Both use configured scan batch, operation timeout and poll interval, inherit shutdown
cancellation and are joined during drain. Consumption uses operation-ID keyset pages;
unknown/error rows advance the cursor, empty tails wrap, and process restart begins
again. Late inserts below a cursor are revisited. Every settlement rechecks immutable
owner proof in its own transaction, so concurrent nodes cannot double-charge/refund.
No timer, terminal Run status or absent usage manufactures a zero-consumption fact.

The bounded `consumption_reconcile` metric distinguishes this loop from Run quota;
ordinary waiting remains metrics-only. Generic scan tests cover failed and unknown
early rows, late keys, cancellation and concurrent nodes. A separate real bootstrap
loop test composes real Budget/Execution owners, settles persisted20 after Stage
failure and admits another80 under fixture cap100. The policy authority used to
seed reservations remains a fixture; the production recovery loop grants no calls.

## Failed output does not erase completed model consumption

A fully drained, complete provider usage report now survives later invalid Final,
overlay/snapshot or runner cleanup failure. The SDK adapter clears failed output
and snapshots, returning only accounting evidence alongside the original error.
The runtime adapter preserves those counters without setting a stageable result
identity. Processor records known usage before terminalizing the failed Attempt;
it never stages or completes that failed result. Unknown/incomplete streams remain
held. Cancellation or incomplete SDK drain does not create a final usage proof.

Real SDK/HTTP tests first reproduced lost reported and explicit-zero usage after
empty-Final rejection. Runtime tests verify the accounting-only error result and
Stage rejection; Processor tests verify persistence without success promotion.
These are distinct layer tests, not live provider or full database execution proof.

## Published model cap (authority integration pending)

The Control quota document now optionally publishes `max_total_model_tokens`.
It is a cumulative tenant model-token cap across policy revisions, not pricing or
an implicit time window. Missing means unconfigured, zero means no new consumption.
The wire digest binds the field. This is the configuration source for the future
current-policy/bound authority, not an implemented adapter or new-call permission.
Existing Run quota logic does not substitute this scalar for concurrency limits.

## Pending-bound current model cap reader

Execution's `PendingRouteAuthority.ReadModelBudget` now returns the quota reference
and cumulative cap only after validating its persisted input, source scope/epoch,
current principal/access policy, selected Session policy and real Manifest target.
The same caller transaction holds the current snapshot head and rechecks database
freshness after target resolution and document decoding. Missing budget remains
not-ready; explicit zero is preserved for the reservation owner to deny new cost.
Changed session/tenant/binding/actor, revoked principals and expired snapshots never
return usable policy evidence. This read creates no Run, Attempt or reservation.

The real PostgreSQL test installs independently supplied source fixtures through the
actual projection and reads persisted pending/Manifest data. It verifies newer caps,
zero, missing, revocation and stale state. The source HTTP authentication remains a
fixture. This is not the complete ConsumptionAuthority: enforceable call bounds and
atomic PendingRun reservation/dispatch still need implementation and composition.

## One-time durable dispatch transition

`MarkExecuting` now succeeds only for PREPARING with no agent_started_at. It returns
fenced for a repeated same-grant transition, including after uncertain commit or
process restart; a prior successful transition is never a new physical-call permit.
Migration0017 rejects legacy repeated status updates, PREPARING regression and
rewriting the start timestamp. Lease renewal and usage persistence remain valid.

This is at-most-one successful dispatch transition, not exactly-once network
execution. A crash after commit but before send may leave an unknown charged hold;
recovery must not blindly resend or refund it. New actual calls require new Attempt
identities and their own budget. The production bound/admission implementation is
still pending; this fence does not create it or enable managed claims.
