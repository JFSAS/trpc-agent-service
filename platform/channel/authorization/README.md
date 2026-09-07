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
