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
- subject;
- payload up to 1 MiB;
- `traceparent` and `tracestate`;
- attempt count;
- availability time;
- lease token/deadline;
- published/failed timestamps.

`event_id` is unique and becomes the external message deduplication ID. It
must be canonical text with no leading/trailing ASCII whitespace or CR/LF
characters. This guarantees the durable application ID is not normalized when
the default NATS dispatcher serializes it as `Nats-Msg-Id`.

## Propagation metadata

Only W3C `traceparent` and `tracestate` are persisted.

OpenTelemetry Baggage is intentionally discarded at the outbox boundary because
it can contain credentials, personal data, or high-cardinality metadata and
would otherwise become durable database content.

When the runtime dispatcher is wired to telemetry, it reconstructs the stored
trace context before publishing. The NATS publisher then creates its normal
publish span and injects a fresh message propagation context.

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

Each claimed batch is dispatched concurrently. The configured batch size is
therefore also the upper bound on dispatcher publish concurrency.

If any event returns a fatal publisher/storage/settlement error, the dispatcher
cancels the shared batch context immediately. Sibling publish calls that honor
context stop early instead of creating additional broker side effects while
durable state is already known to be uncertain. The dispatcher still drains all
batch results so every canceled claim can attempt its bounded retry/settlement
transition before the fatal error is returned to the process lifecycle.

The default dispatcher bounds are:

- batch size: 8;
- poll interval: 500ms;
- lease: 30s;
- max attempts: 10;
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
Shutdown timeout is different: shutdown only needs to finish work that is
already claimed, so it must strictly exceed publish timeout + one store timeout.

## Retry and terminal failure

Broker publish errors do not crash the service when the durable state transition
succeeds.

For a normal publish failure:

1. if the attempt limit is not reached, the lease is released with exponential
   retry delay capped by `OUTBOX_DISPATCH_RETRY_MAX_DELAY`;
2. when `OUTBOX_DISPATCH_MAX_ATTEMPTS` is reached, the row is marked failed.

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

## Settlement

Settlement requires both row ID and current lease token.

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
after fatal settlement failure, exponential retry capping, cancellation retry,
and sanitized settlement failures.
