# Control events V1

`route-projected.schema.json` is the authoritative, self-contained route event
contract for Channel Gateway. `fixtures/valid` and `fixtures/invalid` exercise
both enabled snapshots and disabled tombstones. The event contains no credential
material; unknown fields are rejected, including token or secret fields.

## Generation semantics

- `event_id` is a permanent producer event identity, not a routing generation.
- `(provider, account_id)` identifies the projection. V1 supports `telegram`
  and `wecom` and one binding target per account.
- A positive `route.generation` replaces the entire previous snapshot. It is the
  monotonic account route sequence, not the referenced Binding entity revision.
  Switching Binding, disabling, and re-enabling the same account never reset it;
  the Control publisher must allocate a newer account generation for each change.
  Publisher implementation remains pending.
- An enabled snapshot includes tenant, binding, immutable deployment revision,
  manifest reference, and `sha256:` plus 64 lowercase hex digest.
- A disabled tombstone contains only provider, account, generation. It does not
  preserve a hidden target. Re-enabling requires a newer complete snapshot.
- Exact event replays succeed, including after the projection advances. Reusing
  an event ID with changed content conflicts.
- Lower generations are durably receipted and ignored. Equal generation with
  identical projection content is idempotent; changed content conflicts.
- Receipt and projection changes commit together before a consumer ACK.

Manifest references are opaque immutable object references (`urn:` or path-like
IDs). Query strings, fragments, userinfo, and credential-bearing URLs are not
part of this event contract. The Worker resolves the reference through its owned
manifest port; Gateway never resolves mutable Control Draft/latest state.

## Wire generation and decoding

From the repository root:

```sh
go generate ./api/events/control/v1
go test ./api/events/control/v1/... ./gen/events/control/v1/...
```

The local stdlib-only generator derives Go transport structs and the schema hash
in `gen/events/control/v1/route_projected.gen.go`. Conditional required fields,
size bounds, and enum values remain enforced by the JSON Schema.
`DecodeRouteProjectionEvent` validates the complete event (maximum 16 KiB) against
that schema before decoding the generated DTO. Inbound adapters explicitly map
wire DTOs to the Routing domain; domain/application do not import generated DTOs.

## Gateway replay and admission gate

V1 consumes **one completely retained subject**, `control.channel-route.v1`.
The transport supplies stream name, stream instance (`StreamInfo.Created`), and
stream sequence from trusted JetStream metadata; those fields are not accepted
from the event payload. Root topology uses file-backed Limits retention and
prevents delete/purge/TTL/rollup or finite retention from creating history gaps.

Before opening Admission handlers, bootstrap calls `Consumer.Initialize(ctx)`.
That operation captures the stream's current last sequence as the startup target
and runs `BeginReplay`. Only a contiguous durable prefix through that target
initializes Routing; finding any existing projection row is not sufficient.
An empty retained stream initializes at sequence zero. Multi-replica out-of-order
receipts may update generation-monotonic projections, but gaps hold the durable
checkpoint until all earlier positions are applied. Event receipt, stream receipt,
projection update, and checkpoint commit in one PostgreSQL transaction, then ACK.

`QueryProjectionHealth`, `Resolve`, and the Admission transaction's
`VerifyGeneration` enforce initialization/quarantine/staleness. The consumer
rechecks stream identity and full retention before each event. It observes an
idle stream at least every 30 seconds; the first implementation grants at most
5 minutes since the last trusted source observation. A quiet business account
therefore stays healthy, while a prolonged disconnected source stops new
Admissions. Observation time is captured before database lock waits, capped by
database time, and merged monotonically across replicas. A delayed lower-sequence
observation does not falsely classify a healthy stream as permanently truncated.

Permanent malformed/schema-conflicting events, generation/sequence conflicts,
missing retained history, stream recreation, and corrupt or unbound legacy
projections create durable quarantine records containing only a typed reason and
payload digest. These records block new Admissions globally and survive process
restart. Invalid input receives a transport ACK only after quarantine persistence;
transient PostgreSQL/transport errors remain retryable and unacknowledged.
Quarantine removal and deliberate source rebuilding require a separate reviewed
recovery operation; normal replay never silently resets source identity or clears
quarantine. The read-only generation verifier returns a corruption error. The next
`QueryProjectionHealth` audit persists the isolation record; a synchronous
transaction-failure bridge must invoke that audit after rollback, never while
holding Admission locks.
