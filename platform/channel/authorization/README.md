# Current authorization transport and installation

This public package contains shared current-state ports, not admission/execution
capabilities. A Reader supplies a complete, authenticated current source snapshot;
it must not synthesize one from an AdmissionAuthorization observation.

`postgres.New(pool, scope, epoch, namespace)` supports only the compiled Gateway
and Worker table families. Each service supplies its own owned database pool and
migrations. Namespace is not an arbitrary SQL identifier. Reader HTTP identity and
workload assignment remain composition responsibilities; the root ports/installer create no
migrations, topology or deployment unit. `controlhttp` provides the explicitly
configured mTLS reader; `refresh` provides the bounded lifecycle scheduler.

The installer is shared rather than copied: bounded streaming proof, transaction
staging, row-derived digest rechecks, fixed DB-time expiry, supersession and sticky
source/version/content conflict handling apply identically to both owners.
Gateway adapters retain their existing API and errors. Worker migration 0006 owns
its own snapshot/principal/staging tables; no Worker query reads Gateway tables.

Installing a valid snapshot does not prove SessionPolicy/TenantQuota dependencies,
model/tool isolation or the complete F01–F12 gates. In particular, Worker opt-in bootstrap binds its independent source lifecycle and Claim checks
its own projection. The transitional zero-Attempt hold remains until full
dependency and execution gates are implemented.

## Immutable policy dependencies

`controlhttp.Client.ReadPolicyDependencies(ctx, verifiedAccessPolicy)` resolves
only the exact SessionPolicy and TenantQuota referenced by that policy. The public
wire decoder verifies complete owner envelopes independently of Control internals.
`MatchPolicyDependencies` checks tenant/kind/ID/revision/digest and returns detached
content. It expects a previously verified AccessPolicy; it does not grant access,
check enabled/partition rules, reserve capacity, or reset any freshness deadline.
Opt-in Gateway/Worker refresh now uses `CompleteReader` before atomic snapshot
installation. Ordinary hot-path messages do not synchronously call the resolver.

## Atomic complete snapshot extension

`CompleteReader` fetches dependencies inside the original current-read deadline.
DENY_ALL needs none. All other policies require a complete exact pair; partial,
late or failed reads return no result. The original monotonic timestamps survive
unchanged. The installer separately anchors DB time before *all* network I/O.

Gateway migration 0017 and Worker migration 0008 add paired dependency JSONB to
each owner's current head. Both documents, policy and principals commit in the
same transaction. A prior head may be enriched at the same generation; once its
exact policy has complete dependencies, an old/incomplete writer cannot erase or
reinterpret them. DB constraints bind both kinds/tenant/references; the installer
redecodes actual stored JSONB before commit to detect trigger/storage corruption.

Legacy/source-only callers may still install NULL dependencies, which is explicitly
incomplete rather than authorization-ready. Production opt-in refresh uses the
complete reader. This increment does not turn local subject checks into enabled,
session, budget or execution grants; the Worker zero-Attempt hold remains.

## Runtime dependency checks

`CheckPolicyDependencies` verifies both stored envelopes and their exact references
before making a definitive enabled/denied decision. Missing, corrupt or substituted
content is unknown, never an implicit allow. Disabled SessionPolicy or Quota is a
known denial. Verified constraints preserve ordinary zero limits literally; no
unlimited or reserved-capacity interpretation is invented here.

For PUBLIC_LIMITED, the semantic checker requires per-user partitioning, explicit
public quota intent, positive limits and an explicit valid local ceiling. Missing
ceiling is not-ready, excessive limits or shared partition are denied. That check
alone does not implement tool isolation, session storage or reservations; production
PUBLIC_LIMITED stays unenabled until those gates are wired.

Admission and Worker now read paired dependency JSONB with the same locked current
head and invoke this checker before an ALLOW/local check success. Known principal
or account denial still requires no dependency grant. Missing dependency state is
retryable; known dependency denial produces no Run in Admission. Worker allocates
zero Attempts even when all local document checks pass, pending full runtime gates.
