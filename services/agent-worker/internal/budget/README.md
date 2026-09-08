# Worker budget owner: shared Run-count reservation

This module currently implements ONLY the published Quota definition's
`max_concurrent_runs` and `max_runs_per_minute`. It is not a model-token, tool-cost,
Provider-delivery or billing budget, and does not implement `PendingRunReservation`.
A production Run reservation must compose all required dimensions; no adapter silently
substitutes Run counts for a bounded model-cost reservation.

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
The production settlement scheduler and retained-history/tombstone retention remain
unimplemented. The component is not production-enabled.

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
