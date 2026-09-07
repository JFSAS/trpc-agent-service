# Policy notification consumer

This adapter connects retained JetStream notifications to the exact mTLS reader
and PostgreSQL policy-history store via interfaces. `Step` and `Run` are callable;
Bootstrap is wired; explicit policy_scopes declarations drive normal reconcile and
exact ACL generation. Default scopes are empty and projection is disabled.

## Delivery semantics

1. Verify the pinned stream creation identity and exact subject/configuration.
   The retained stream must be complete from sequence 1, with file/limits retention,
   discard-new, 64 MiB total, 16 KiB messages, no deletion/purge/TTL/transform/source.
2. Verify an explicit-ACK, deliver-all pull durable with one pending ACK and unlimited
   delivery attempts. DurableName is SHA256(scope)-derived: different scopes must
   never share a durable, while replicas of one scope share it.
3. Read one message and recheck stream/consumer identity plus trusted JetStream
   metadata, subject, closed schema and publication event type.
4. For this scope, reject a mismatched source epoch, fetch the exact document and
   commit it through the store, then commit its per-position digest/cursor before
   DoubleAck. Exact checkpoint replay skips the external reader; a missing predecessor
   or conflicting position stops consumption. Same-version replay remains idempotent.
5. For another scope, validate the event before ACK but never fetch/store it. That
   scope's separate durable independently processes the retained notification.

Invalid input, source drift, read failure, commit failure and ACK failure return
fixed diagnostic codes. Messages are not TERM'd or silently dropped; Run stops on
these errors and leaves unacknowledged work for retry/recovery. Timeout on an idle
pull is not an error. Cancellation ends within the bounded pull and operation waits.

## Boundaries

ACK means historical processing only. A retained document with a gap may be ACKed
because the gap is durably represented, not because authorization is ready. The
adapter creates no freshness timestamp, Run or principal grant. Source creation is supplied by the caller and checked against a durable PostgreSQL
pin before pulling, during message handling and before ACK, including comparison of the broker ACK floor with local processing. Recreating a same-named
stream or changing the epoch persists SOURCE_CHANGED instead of adopting the new
source. Existing unbound history is blocked as UNBOUND_HISTORY. The persistent processing cursor is checked separately; neither identity nor cursor
provides a complete current snapshot or freshness proof. Snapshot/operator recovery
remain required before production authorization.

The integration test uses a real disposable JetStream broker, real loopback mTLS
Gateway reader and real Gateway PostgreSQL migrations/store in one path. The HTTP
endpoint is a policy-response fixture, not the Control binary. Failed read and
failed deferred commit keep the actual broker ACK pending; retrying the same
obtained delivery and succeeding produces a real DoubleAck. The test does not
claim a broker-timed redelivery or production NATS ACL validation. Production
reconcile/ACL, lifecycle/supervision, cursor/snapshot recovery and full authorization
remain separate work. Existing deployments and Bots are unchanged.
