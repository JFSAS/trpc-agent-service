# Telegram Final Sender Adapter

This adapter directly imports the pinned `github.com/go-telegram/bot v1.25.0`.
It implements Delivery's `SenderProvider` / `ReservedSender`; it is not a
credential owner, another workload, a Worker, or a replacement for A2.

```go
provider, err := telegramadapter.NewProvider(configuredAccountBots)
// Check err, then inject provider into application.NewDispatcher.
```

The constructor accepts a preconfigured `map[string]*bot.Bot`, copies the map,
and performs no HTTP. Empty configuration is permitted; an unconfigured account
is unavailable. The composition owner must establish each bot's authenticated
account identity, configure bounded HTTP transport and sanitized SDK handlers,
and never call `SetToken` or mutate SDK options concurrently with use. This
adapter does not invent credential lookup, rotation or authentication from a
non-nil client. Its tests skip `getMe` only for synthetic local HTTP fixtures.

`Reserve` validates the fixed original Admission target and exact persisted
part/digest without contacting Telegram. It captures a one-shot handle. Only a
matching A2 Attempt may enter `SendFinal`: claim token, intent/part, instance,
request ID/digest, positive attempt number and nonempty attempt ID/evidence
capability are checked. A local handle cannot verify that a remote database
transaction committed; the Application/real-PG integration must enforce the
A2-only calling path. Telegram uses no Connection owner lease.

Each handle admits at most one concurrent SDK call. `Release` is idempotent,
prevents later sends and cancels an in-flight call. The call's context is bounded
by the caller, the immutable Final deadline, `CallingUntil`, and a one-minute
adapter ceiling. Calling after cancellation returns `NOT_SENT/deadline`; after
entering `SendMessage`, cancellation, transport failures and malformed responses
return `UNKNOWN`, never proof of non-transmission.

The request fixes `chat_id`, optional `message_thread_id`, original reply message
and plaintext part. It does not set parse mode or allow sending without the
original reply target. A migration response does not change the chat. Positive
results require a valid message ID, the original chat ID and, when requested,
the original topic. Typed SDK 400/401/403/404/409 and migration failures become
`REJECTED/permanent`; typed 429 becomes `REJECTED/rate_limited`. No raw SDK error,
HTTP URL, response description or body enters a Domain Result. An untyped failure
remains `UNKNOWN`; Delivery's retry policy never retries an UNKNOWN Final.

`PlanText` owns Unicode-preserving Telegram chunks of at most 4,096 code points.
The adapter sends only the exact precommitted part; the ledger owns ordering,
per-part certainty, Final barrier and recovery. The SDK's `raw_request.go` uses
`io.ReadAll`, so the supplied HTTP transport's response-body budget is a real
composition requirement, not a guarantee supplied by the SDK itself.

The local `httptest` suite exercises exact multipart parameters, no reserve
side effect, one-shot concurrency, fixed-target/attempt fencing, cancellation,
release, typed rejection/migration, malformed and wrong-target responses,
connection loss and persisted multipart text. These tests do not contact the
real Telegram API or establish production credential/IM acceptance.
