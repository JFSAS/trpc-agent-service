# Access policy notification relay

The Channel module exposes `NewAccessPolicyRelay(publisher)` using its own fixed
scope/catalog epoch. Callers cannot substitute another catalog identity through
this factory. Control Bootstrap now assembles and runs the relay when Channel is enabled,
and cancels/drains it within the existing shutdown deadline before closing shared
resources. Deployment declarations provision the retained policy stream and grant
only its exact publish subject to Control. No extra process is required. Gateway
policy consumption, snapshots and projection remain pending.

The PG adapter claims only ChannelAccessPolicy published notifications, joining
immutable policy revision and tenant/account ownership to determine scope. It
uses SKIP LOCKED, 15-second leases, unique random claim tokens and attempt counts.
Completion requires the exact unexpired token/attempt/event/revision. Audit,
definition and route events stay outside this claim queue.

The relay verifies closed wire schema, configured scope/epoch, all row identity
fields and transport digest before sending. Integrity failures become retained
FAILED records. Transient publish failures retry with exponential delay bounded
at one minute and a fixed sanitized error code. A publish attempt has a five-
second deadline; success requires a durable ACK from the exact policy stream.
The ACK-before-database-finish crash window is intentionally at-least-once:
JetStream dedup helps inside its window, and downstream durable identity checks
remain mandatory. This is not external exactly-once delivery.

Tests use an explicitly disposable PostgreSQL and NATS server. They verify
lease reclaim, stale completion rejection, sanitized retry, corrupted claim
quarantine, real durable ACK, duplicate message suppression and cross-tenant
Msg-Id isolation. Reference validation is a named fixture in this relay test;
no public-tool isolation or runtime authorization claim follows from it.
