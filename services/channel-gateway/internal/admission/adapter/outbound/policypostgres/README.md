# Gateway policy history projection

`Store.Apply` persists a previously authenticated published notification and its
verified immutable document into Gateway PostgreSQL. It pins scope/epoch, rechecks
all notification/document identities and the complete shared document digest.
No Control tables or business repositories are read.

## Transaction and continuity

- Migration `0012_access_policy_projection.sql` adds account-local projection heads
  and immutable document history. Existing migrations are unchanged.
- The first INSERT/ON CONFLICT and account head FOR UPDATE serialize all same-account
  writers, including concurrent first delivery. Document/head updates commit together.
- observed_revision is the maximum retained revision; contiguous_revision is the end
  of the retained chain starting at revision 1. Arrival 3,1,5,2,4 advances continuity
  as 0,1,1,3,5. Old/repeated arrivals never reduce either number.
- A missing revision is a recoverable history gap; storing later documents does not
  certify completeness. A snapshot protocol may eventually establish a later verified
  baseline without retaining all historical revisions; that protocol is not present.
- Same-revision digest conflicts or account identity changes commit a permanent
  blocked_reason and return POLICY_PROJECTION_BLOCKED. Replays/restarts cannot clear
  this marker. A future operator recovery protocol must explicitly reconcile it.
- SQL triggers preserve immutable documents, identity and monotonic head revisions.
  Same-revision replay revalidates the persisted body, not just the indexed digest.
- ReadExact uses repeatable-read and verifies the stored document again. Tenant,
  scope and epoch misses return no document. Blocked accounts return no document.
- Commit failures return zero results. Operations have a 5s context deadline;
  rollback uses an independent bounded context.

## Not authorization readiness

No applied_at/fresh_until or current principal state is created here. A contiguous
local chain cannot prove no more recent Control revision or revocation exists.
ReadExact is a historical read, not an Admission authorization guard. It must not
be used to reset freshness or authorize a new Run. The source stream cursor and
complete snapshot/watermark, principal projection, freshness fence, Admission
transaction guard, and Worker checks remain to be implemented.

The store and HTTP reader are not yet wired into a running consumer. Tests use a
real isolated PostgreSQL migration/store, including reverse concurrent arrival,
gap filling, immutable SQL guards, persistent conflicts, cancellation, corrupt
first-writer fixtures and a deferred commit-failure trigger. They do not prove a
broker-to-Admission production chain or real Bot authorization.

## Durable source identity

Migration `0013_policy_source_identity.sql` adds a scope-owned source pin. BindSource
records source epoch, exact stream name and creation timestamp before writing policy
history. Repeated identical bindings succeed; changed identity persists SOURCE_CHANGED.
Existing unbound heads persist UNBOUND_HISTORY and require explicit recovery. SQL
triggers reject identity mutation, deletion and clearing a block.

Apply now requires a valid source and holds FOR SHARE on that source row through
its transaction; BindSource uses FOR UPDATE. Source blocking therefore serializes
with history writes rather than racing past commit. ReadExact checks existing source
blocks within its read-only snapshot. No freshness deadline is created by binding.

## Contiguous broker processing checkpoint

Migration `0014_policy_processing_checkpoint.sql` adds processed_sequence,
observed_sequence and immutable per-position canonical event digests. Processed
checks the exact receipt before replay bypasses Control; sequence <= cursor alone
is never accepted. RecordProcessed verifies the committed local policy document
(or only the notification for a foreign scope), then inserts its position receipt
and advances the contiguous cursor in one source-locked transaction. A document
may have committed earlier; a crash in that interval safely replays Apply.

Higher positions with missing predecessors record the observed high-water mark
but return POLICY_PROCESSING_GAP without advancing the cursor. Conflicting content
at an already processed position persists POSITION_CONFLICT. ObserveBroker also
rejects a broker ACK floor ahead of the database, including an idle durable after
a database restore. SQL guards reject cursor regression and receipt mutation.
These cursors still do not prove current principal authorization or create a TTL;
complete snapshot, source recovery and retention/compaction remain future work.
