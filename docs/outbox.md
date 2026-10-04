# Transactional Outbox

The outbox closes the database/message-intent atomicity gap without making
PostgreSQL storage depend on a broker.

It has two distinct layers:

- durable PostgreSQL enqueue/claim/settlement primitives;
- an optional bounded runtime dispatcher.

Durable enqueue remains available whenever PostgreSQL is available.
`OUTBOX_DISPATCH_ENABLED=false` only disables the background dispatcher.

## Transaction boundary

Use the same caller-owned `pgx.Tx` for the application mutation and
`outbox.Enqueue`:

```go
err := database.InTx(ctx, pool, func(tx pgx.Tx) error {
    if err := repository.Update(ctx, tx, ...); err != nil {
        return err
    }
    return outbox.Enqueue(ctx, tx, outbox.Event{
        ID:      eventID,
        Subject: "example.updated",
        Payload: payload,
    }, propagator)
})
```

The outbox helper never starts a transaction implicitly.

## Stored fields

Each event stores:

- stable application-supplied `event_id`;
- subject, stored as bounded valid UTF-8 text;
- payload up to 1 MiB; a nil Go byte slice is normalized to an explicit
  zero-byte payload rather than SQL NULL;
- `traceparent` and `tracestate`;
- attempt count;
- availability time;
- lease token/deadline;
- published/failed timestamps.

`event_id` is unique and becomes the external message deduplication ID. It
must be canonical valid UTF-8 text with no invalid UTF-8 byte sequences,
NUL, leading/trailing ASCII whitespace, or CR/LF characters. This keeps the durable
application ID on the same stable text contract used by the default NATS
dispatcher when it becomes `Nats-Msg-Id`.

The durable subject must contain 1-255 bytes, be valid UTF-8, and exclude
NUL before PostgreSQL is touched. The outbox deliberately does not validate NATS wildcard
or token grammar: it remains transport-neutral, and a configured publisher owns
transport-specific subject validation.

PostgreSQL CHECK constraints use `octet_length` for the same event-ID, subject,
`traceparent`, and `tracestate` byte ceilings enforced by Go. The event-ID
constraint also mirrors the canonical message-ID rule: leading/trailing ASCII
space, TAB, CR, or LF is rejected, as are CR/LF anywhere in the identifier.
Internal TAB remains valid. Direct SQL, legacy writers, or custom persistence
code therefore cannot bypass the durable ID/text contract.

## Propagation metadata

Only W3C `traceparent` and `tracestate` are persisted.

OpenTelemetry Baggage is intentionally discarded at the outbox boundary because
it can contain credentials, personal data, or high-cardinality metadata and
would otherwise become durable database content.

When the runtime dispatcher is wired to telemetry, it reconstructs the stored
trace context before publishing. The NATS publisher then creates its normal
publish span and injects a fresh message propagation context.

Propagation is observability metadata, not durable business state. Panics from
an injected propagation hook are contained at the outbox boundary: enqueue
continues without partially written trace metadata, and dispatch falls back to
the original context instead of failing the durable event. Restored propagation
may add trace values, but the dispatcher context remains authoritative for
deadline, cancellation, and cancellation cause.

Persistence applies the same fail-open policy to unsafe propagation text.
Oversized, invalid-UTF-8, or NUL-containing `traceparent` is dropped together
with `tracestate`; an unsafe `tracestate` is dropped independently while a
safe `traceparent` is retained. Metadata is never byte-truncated into another
trace value.

## Claiming

`Store.Claim` uses PostgreSQL `FOR UPDATE SKIP LOCKED` and commits the claim
before any external publish call.

A claim:

- increments the attempt count;
- assigns a cryptographically random lease token;
- sets `locked_until`;
- returns only rows whose availability time has passed and whose prior lease is
  absent or expired.

This permits multiple dispatcher instances without holding a database
transaction open during broker I/O.

The runtime dispatcher bounds each claim operation by
`OUTBOX_DISPATCH_STORE_TIMEOUT`.

## Dispatcher

The dispatcher owns no NATS-specific type. It accepts a transport-neutral
publisher function:

```go
type Publisher func(
    context.Context,
    string, // subject
    string, // stable event/message ID
    []byte, // payload
) error
```

The application composition root adapts `messaging.Client.Publish` to this
function only when PostgreSQL, NATS, and the dispatcher profile are all enabled.

`Dispatcher.Run` rejects nil and zero-value dispatchers with an explicit error
instead of dereferencing missing runtime dependencies. `NewDispatcher` remains
the supported constructor and validates the store, publisher, and bounded runtime
configuration before execution.

Each claimed batch is dispatched concurrently. The configured batch size is
therefore also the upper bound on dispatcher publish concurrency.

If any event returns a fatal publisher/storage/settlement error, the dispatcher
cancels the shared batch context immediately. Sibling publish calls that honor
context stop early instead of creating additional broker side effects while
durable state is already known to be uncertain. The dispatcher still drains all
batch results so every canceled claim can attempt its bounded retry/settlement
transition before returning.

When multiple batch members independently report fatal errors, the dispatcher
returns an `errors.Join` aggregate rather than discarding all but the first
cause. This preserves `errors.Is` / `errors.As` visibility for every fatal
settlement cause while keeping the existing sanitized operation-error strings.

The default dispatcher bounds are:

- batch size: 8;
- poll interval: 500ms;
- lease: 30s;
- max attempts: 10, bounded by PostgreSQL `INTEGER` max;
- retry base delay: 1s;
- retry max delay: 1m;
- publish timeout: 5s;
- store operation timeout: 2s;
- shutdown timeout: 10s.

The lease must strictly exceed the full claim-to-settlement budget:

- one store timeout for the claim transaction;
- one publish timeout;
- one store timeout for settlement.

The lease starts while the claim transaction is executing, so omitting claim
time can leave too little lease remaining for a bounded publish plus settlement.

PostgreSQL lease intervals are persisted as integer microseconds. Validation uses
that persisted precision rather than Go's nanosecond duration precision. A
configuration whose only safety margin is less than one microsecond is rejected,
because that margin would be truncated away before `locked_until` is stored.

Shutdown timeout is different: shutdown only needs to finish work that is
already claimed, so it must strictly exceed publish timeout + one store timeout.

## Retry and terminal failure

Broker publish errors do not crash the service when the durable state transition
succeeds.

For a normal publish failure:

1. if the attempt limit is not reached, the lease is released with an
   exponentially increasing retry envelope capped by
   `OUTBOX_DISPATCH_RETRY_MAX_DELAY`;
2. the first retry uses `OUTBOX_DISPATCH_RETRY_BASE_DELAY` exactly; later
   retries normally use deterministic per-event jitter in the final 25% of the
   current envelope (75-100% of the envelope). If the capped envelope is close
   to the configured base, the jitter lower bound is clamped to
   `OUTBOX_DISPATCH_RETRY_BASE_DELAY`, so a later retry is never scheduled
   sooner than the first retry;
3. when `OUTBOX_DISPATCH_MAX_ATTEMPTS` is reached, the row is marked failed.

`OUTBOX_DISPATCH_MAX_ATTEMPTS` may not exceed PostgreSQL `INTEGER` max
(2,147,483,647), because each claim increments the durable `attempts INTEGER`
column before dispatch. Rejecting larger Go `int` values prevents a configured
retry budget from outliving the database representation and failing with an
integer overflow during claim.

The jitter key is the stable `event_id` plus attempt number. It requires no
process-global RNG, is reproducible across restarts, and spreads different
events that failed together instead of scheduling the entire batch for the same
`available_at`. Jitter is quantized to PostgreSQL's microsecond interval
precision, never falls below the configured retry base delay, and never exceeds
the configured retry maximum.

Retry base and maximum delays must themselves be representable as whole
microseconds. `Store.Retry` enforces the same boundary for direct callers. This
prevents PostgreSQL interval serialization from silently truncating a requested
retry delay and scheduling a durable retry earlier than the configured base or
outside the runtime/configuration contract.

Shutdown cancellation is intentionally different: an ambiguous canceled publish
is released with the exact base delay so shutdown recovery stays prompt and
predictable.

If application shutdown cancels an in-flight publish, the dispatcher schedules a
retry even when the attempt count is already at the configured limit. Shutdown
cancellation makes the broker outcome ambiguous, so permanent failure would be
unsafe.

Storage/claim/settlement failures are different: the dispatcher returns a
sanitized runtime error so the process lifecycle can stop rather than pretending
durable state is still trustworthy. Shutdown cancellation does not suppress a
lease-release or settlement failure; uncertain durable state remains a fatal
runtime error even while the process is stopping.

A panic from the injected publisher is also treated as a fatal infrastructure
failure. The panic value is discarded, no published/retry/failed settlement is
written, and the current lease is allowed to expire so a later healthy
dispatcher can retry the event with the same stable event ID.

The injected `EventStore` boundary follows the same process-safety rule. A
panic from claim or settlement code is converted to a sanitized fatal dispatcher
error instead of escaping a background goroutine. The dispatcher does not guess
whether a panicking store mutated durable state; it stops and leaves existing
lease/transaction recovery semantics to the store implementation.

## Settlement

Settlement requires both row ID and current lease token. Settlement methods
reject non-positive row IDs and missing lock tokens before PostgreSQL is touched,
so caller defects are not misclassified as lease-loss or database failures.

- `MarkPublished` records successful dispatch.
- `Retry` clears the lease and moves `available_at`.
- `MarkFailed` clears the lease and permanently stops automatic dispatch.

Every dispatcher store operation is bounded by
`OUTBOX_DISPATCH_STORE_TIMEOUT`.

Settlement requires an unexpired lease as well as the matching token. A worker
cannot settle after its lease expires, even before another dispatcher reclaims
the row; a later claimant receives a fresh token.

## Delivery semantics

The system is **at-least-once**.

A process may publish successfully and crash before `MarkPublished`; the event
can later be claimed again. The dispatcher therefore always publishes with the
stable `event_id` as the transport deduplication ID and consumers must remain
idempotent.

JetStream duplicate windows reduce duplicate delivery but do not turn this into
exactly-once semantics.

The 1 MiB outbox payload bound is a durable-storage limit, not a promise that
every configured broker/stream can transport a 1 MiB payload. NATS applies its
message limits to headers plus payload. The messaging adapter therefore
preflights the final encoded-header upper bound against the connected broker and
verified stream limits before attempting a publish. Deployments using the
dispatcher must size their NATS limits with transport-header headroom above the
largest outbox payload they intend to enqueue.

## Permanent publisher rejections

The outbox package remains transport-neutral. A composition adapter may wrap a
publisher error with `outbox.MarkPermanentPublishFailure` only when it can
guarantee that retrying the unchanged durable event cannot succeed.

Permanent publish failures are settled immediately with `failed_at`, regardless
of the current attempt count. They do not consume the remaining exponential
retry budget. The wrapper preserves the underlying cause for
`errors.Is/errors.As`, but its normal `Error()` text is the sanitized
`outbox publish permanently rejected` sentinel.

The NATS composition classifies only immutable durable-event defects as
permanent:

- invalid literal publish subject;
- invalid canonical message ID.

Size/capacity rejection remains retryable. Broker limits can change
operationally, and managed JetStream limits can drift before an operator restores
or intentionally migrates them. The dispatcher therefore uses its normal finite
retry budget for `ErrMessageTooLarge` instead of marking the durable event
failed immediately.

Transient connection/broker failures remain on the same finite retry path.

## Application lifecycle

The dispatcher is started only when:

- `DATABASE_ENABLED=true`;
- `NATS_ENABLED=true`;
- `OUTBOX_DISPATCH_ENABLED=true`;
- the HTTP server is constructed and its configured TCP address has been bound
  successfully.

This ordering prevents an HTTP bind failure such as an already-used port from
occurring after durable events have already started publishing.

Normal shutdown order is:

1. stop/drain HTTP;
2. cancel and wait for the outbox dispatcher within
   `OUTBOX_DISPATCH_SHUTDOWN_TIMEOUT`;
3. drain NATS;
4. close PostgreSQL;
5. flush/shut down telemetry.

This keeps the broker and database alive while a claimed batch is being
released, published, or settled.

## Configuration

```dotenv
OUTBOX_DISPATCH_ENABLED=false
OUTBOX_DISPATCH_BATCH_SIZE=8
OUTBOX_DISPATCH_POLL_INTERVAL=500ms
OUTBOX_DISPATCH_LEASE=30s
OUTBOX_DISPATCH_MAX_ATTEMPTS=10
OUTBOX_DISPATCH_RETRY_BASE_DELAY=1s
OUTBOX_DISPATCH_RETRY_MAX_DELAY=1m
OUTBOX_DISPATCH_PUBLISH_TIMEOUT=5s
OUTBOX_DISPATCH_STORE_TIMEOUT=2s
OUTBOX_DISPATCH_SHUTDOWN_TIMEOUT=10s
```

A disabled dispatcher ignores dispatcher-specific settings, matching the other
optional profiles.

## Security

Raw PostgreSQL and broker errors are not copied into normal dispatcher error
strings.

Payloads, propagation values, and publisher panic values are never copied into
infrastructure error strings. Only `traceparent` and `tracestate` are durably
persisted by the generic outbox.

## Integration tests

The PostgreSQL/Testcontainers suite verifies:

- enqueue rollback with the caller transaction;
- lease exclusion while a claim is active;
- stale-token settlement rejection;
- settlement rejection after lease expiry;
- delayed retry and new claim tokens;
- monotonic attempt count;
- final publish settlement;
- duplicate event IDs returning sanitized error text.

The combined PostgreSQL + real JetStream integration verifies:

- dispatch to the configured subject;
- stable `Nats-Msg-Id` equal to `event_id`;
- persisted trace context restoration;
- successful PostgreSQL publish settlement;
- bounded dispatcher shutdown.

Unit tests additionally verify bounded claim contexts, strict claim/publish/
settlement lease budgeting, concurrent batch dispatch, sibling cancellation
after fatal settlement failure, exponential retry capping, deterministic
per-event retry jitter, cancellation retry, and sanitized settlement failures.
