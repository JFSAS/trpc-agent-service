# Managed backend descriptor fixtures (P0b1)

These examples describe PostgreSQL, Redis, Qdrant and S3 connection targets;
they do not provision services, grant database privileges or enable Worker roles.
PostgreSQL and Redis entries may both offer Session and Memory on one physical
backend. Catalogue role authorization is checked before deriving a fixed snapshot.
Session uses `tenant-session-v1`; Memory uses `tenant-subject-agent-v1`. Consumers
must call `ValidateForRole` and actually enforce SQL/schema/account or Redis
namespace/ACL isolation. Merely changing a descriptor string grants no database
isolation. Separate scoped credentials may be required by the deployed backend.

The source target's isolation field documents its base shape; role-bound snapshots
are derived deterministically. Their authorization digests differ by tenant/role,
while physical target ID/revision stay fixed. Existing Session snapshots are byte
identical. A consumer must not compare whole role-bound snapshots to decide whether
two roles selected the same physical target.

P0b1 exposes configuration types and Profile eligibility through an injected Port;
Control Bootstrap/Manifest compilation/runtime registration are subsequent batches.
No backend is silently enabled, no runtime client is created by Profile publication,
and absent BackendAccess rejects managed selection rather than trusting syntax.
