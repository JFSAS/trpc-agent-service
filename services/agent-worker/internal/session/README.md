# Worker conversation scope and generation registry

This module implements the F03 scope/generation transaction foundation. It is not
registered on a broker/HTTP route yet, and it does not replace Execution intake
until the current authorization adapter and pending-intake lifecycle are wired.
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
atomicity. It is NOT a complete actual Execution Run intake, a real current-policy
adapter, `/new` transport, accepted-head advancement or process-kill test.
Concurrent reset/replay/CAS, intake-before-reset ordering, reopened registry state,
shared permission denial and audit rollback are covered. Existing Run/Attempt
behavior and zero-Attempt authorization hold remain unchanged.

Next: wire current SessionPolicy/principal authority and exact operation grants,
then integrate durable pending authorized intake and registry target discovery.
This avoids waiting for a projection whose refresh target would only exist after
intake success. Replace the old Execution scope selection in that integration;
connect command receipts/results to Gateway only after that path is verified.

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
