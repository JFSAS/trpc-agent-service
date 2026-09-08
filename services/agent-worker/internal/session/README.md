# Worker conversation scope and generation registry

This module implements the F03 scope/generation transaction foundation. It is not
registered on a broker/HTTP route yet, and it does not replace Execution intake
until atomic pending-to-Run promotion is wired. A concrete pending-input route
authority now exists and is integration-tested, as described below.
The old execution hash is not a selected fallback for the new policy semantics;
that old path must be replaced at the integration step, not preserved as a dual
hash compatibility mechanism.

## Identity

A canonical tagged array includes tenant, provider, account, conversation, topic,
binding, DeploymentRevision and the exact SessionPolicy ID/revision/digest.
`per_user_in_conversation` also includes a stable Principal ID;
`shared_conversation` explicitly has no principal partition. Generation enters the
SessionID hash, not the stable registry key. Policy or deployment changes isolate
history; switching back selects the previously persisted scope's generation.
A Principal named "shared" cannot collide with a shared partition.

## Transactions

- `Registry.WithCurrent` locks/creates the registry, checks current authority,
  passes the chosen SessionID/generation and the SAME transaction to intake, then
  rechecks current authority before commit. Callback writes roll back together.
- `Registry.Reset` serializes command identity, replays an exact immutable command
  result without reapplying a reset, then locks the same registry for new commands.
  Expected-generation CAS, generation advance, command result and audit outbox are
  one transaction. Two commands with the same expected generation cannot both win.
- Actor/scope/expected-generation are bound to command digest. Per-user reset must
  target the caller's partition. Shared reset asks for `session.reset_shared`, not
  ordinary `session.new`. Caller transport authentication is still required.
- A supplied Authorizer must verify current published principal/policy permissions
  and freshness in the transaction. There is no permissive default. The callback
  must neither commit nor retain tx, and must not perform external side effects.
- Migration 0009 starts new scopes at generation 1, disallows identity rewriting,
  requires unit generation increments and immutable command results, and defers a
  command+audit completeness check until commit. This also rejects direct SQL
  generation advances without the corresponding durable result/audit.

## Evidence boundary

Tests use real Worker migrator/runtime roles. A deterministic authorization fixture
exercises denial/final recheck, and a small database intake row proves callback/CAS
atomicity. Additional tests compose the real current-policy adapter below. These
are not complete Execution Run intake, `/new` transport, accepted-head advancement
or process-kill tests.
Concurrent reset/replay/CAS, intake-before-reset ordering, reopened registry state,
shared permission denial and audit rollback are covered. Existing Run/Attempt
behavior and zero-Attempt authorization hold remain unchanged.

Next: compose the verified route-owner adapter to promote a durable pending input through
the registry and Run transaction. Production intake now persists pending inputs
before Session assignment and refresh discovery sees them, so target discovery no
longer requires first creating a legacy Run. The registry is still not the selected
production Run scope: that replacement belongs in atomic promotion, without a dual
hash fallback. Connect command receipts/results only after that path is verified.

## Current Worker projection authorizer

`NewCurrentAuthorizer(scope, epoch, routes)` provides the real principal/policy
side of registry authorization. The source scope/epoch are pinned configuration;
registry identity and saved command content are not capabilities. The authorizer
locks Worker's current head, verifies complete policy/dependency content, exact
SessionPolicy partition/reference, account state, current principal state,
operation scope, authenticated conversation kind/group allowlist and DB-time expiry.
It performs no synchronous Control query.

`session.reset_shared` is now an explicit publishable AccessPolicy operation and
is never implied by `session.new`. Public mode remains not-ready until its other
runtime gates are wired. Scope now carries authenticated conversation kind so a
group is not silently interpreted as private.

RouteAuthority is mandatory and must bind Binding/DeploymentRevision and verified
ingress conversation/actor identity in the transaction; no default grants those
facts. Tests use a route-owner fixture but REAL installed Worker authorization
snapshots, including current revocation. Production route-owner composition and
Execution intake remain pending. New tests prove current projection -> registry,
not the full provider -> intake -> execution/reset accepted-head chain.

## Pending-input route proof

Execution's PendingRouteAuthority binds one immutable pending EventID and pinned
scope/epoch to the requested tenant/account/provider, Binding, DeploymentRevision,
conversation kind/id/topic and admitted Principal. It also rechecks the current
external-user mapping and revision/generation floors, rather than trusting a caller
Principal ID or a stale AdmissionAuthorization. The route port now receives the
operation explicitly: a pending message proves only message.send provenance, not
session.new or shared-reset provenance even if the actor holds those permissions.

A mandatory target verifier is composed by bootstrap from the Manifest owner's
transaction-bound reader and existing complete manifest/Worker-contract verifier.
The Manifest reader holds a shared advisory identity lock until transaction end,
serializing against Apply's conflict poisoning. Execution reads no Manifest SQL.
Missing/conflicted/substituted targets do not grant Session access.

Tests compose real pending intake, installed current projections, complete runtime
manifest verification and the real Session authorizer/registry with Worker roles.
They cover scope/actor/operation substitutions, missing/foreign pending inputs,
external identity remapping, revocation and manifest conflict blocking through the
proof transaction. These are component transactions, not production Run promotion
or reset routing. The pending row must remain until final authorization completes;
atomic promotion still needs a transaction API that consumes it after that check.

## Intake-owned transaction seam

`Registry.WithCurrentInTransaction` now performs selection, caller writes and the
final current-authority check in a savepoint inside an existing owner transaction.
It does not commit the owner's transaction. On success, row and authorization locks
remain held; the owner can consume the input only after the final check, then commit.
On callback/final-check failure, savepoint rollback removes registry/callback writes
even if the caller subsequently commits the outer transaction. The caller must not
perform external I/O or alter proven scope/Run facts between proof and outer commit.

The existing WithCurrent wrapper delegates to this implementation, retaining its
own begin/commit behavior. Real PostgreSQL tests cover commit/rollback, denied final
checks, callback errors, uncommitted invisibility and competing reset lock retention.
The pending-route integration test additionally deletes a real pending input AFTER
its final proof, then rolls back the outer transaction and verifies restoration.
It deliberately does not commit a consumed input without a Run. The actual Run
writer, pending consumption guard, quota reservation and production promoter still
need composition; this API alone is not a completed promotion.


Execution's policy-bound writer now consumes the Selection in the owner callback,
writes a real Run/Receipt and records execution_session_partitions as the immutable
SessionID -> registry scope/generation association. Guarded consumption follows final
authorization. Deferred SQL rejects a commit that leaves the same input pending.
The real PostgreSQL component test commits one such Run and replays its Receipt,
with reservation supplied only by a transactional test fixture. No production
budget owner/promoter is enabled, and execution authorization still allocates zero
Attempts. This supersedes the earlier missing-Run-writer note, not the remaining
reservation/production/accepted-head and full F03 acceptance work.
