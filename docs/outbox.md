# Transactional Outbox

The outbox is a generic PostgreSQL storage primitive for atomically persisting
future messaging work in the same transaction as application state changes.

This first stage deliberately does **not** start a background dispatcher or
depend on NATS. Runtime dispatch/wiring is a separate composition concern.

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

`event_id` is unique and is intended to become the external message
deduplication ID.

## Propagation metadata

Only W3C `traceparent` and `tracestate` are persisted.

OpenTelemetry Baggage is intentionally discarded at the outbox boundary because
it can contain credentials, personal data, or high-cardinality metadata and
would otherwise become durable database content.

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

## Settlement

Settlement requires both row ID and current lease token.

- `MarkPublished` records successful dispatch.
- `Retry` clears the lease and moves `available_at`.
- `MarkFailed` clears the lease and permanently stops automatic dispatch.

A stale/expired worker cannot settle a row once another dispatcher owns a newer
lease token.

## Delivery semantics

The storage model is designed for **at-least-once** dispatch.

A process may publish successfully and crash before `MarkPublished`; the event
can later be claimed again. The next dispatcher stage must therefore publish
with the stable `event_id` as the transport deduplication ID and consumers must
remain idempotent.

JetStream duplicate windows reduce duplicate delivery but do not turn this into
exactly-once semantics.

## Security

Raw PostgreSQL errors are wrapped in operation-only errors so DSNs, SQL, and
driver diagnostics do not enter ordinary logs.

Payloads and propagation values are never copied into infrastructure error
strings.

## Integration tests

The PostgreSQL/Testcontainers suite verifies:

- enqueue rollback with the caller transaction;
- lease exclusion while a claim is active;
- stale-token settlement rejection;
- delayed retry and new claim tokens;
- monotonic attempt count;
- final publish settlement;
- duplicate event IDs returning sanitized error text.

The next stage should add the bounded dispatcher and application lifecycle
wiring as a separate PR.
