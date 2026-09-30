package outbox

import (
	"context"

	coreprop "github.com/Lamy210/go-template/internal/core/propagation"
)

const enqueueSQL = `
INSERT INTO outbox_events (
    event_id,
    subject,
    payload,
    traceparent,
    tracestate
)
VALUES ($1, $2, $3, $4, $5)
`

// Enqueue persists one event intent using the caller-owned DBTX.
//
// To make application state mutation and event creation atomic, callers should
// pass the same pgx.Tx used by their repositories. The helper never starts a
// transaction implicitly.
func Enqueue(
	ctx context.Context,
	db DBTX,
	event Event,
	propagator coreprop.TextMapPropagator,
) error {
	if db == nil {
		return newOperationError("enqueue outbox event", errNilDBTX)
	}
	if err := event.Validate(); err != nil {
		return err
	}

	carrier := injectPropagationSafely(ctx, propagator)
	traceparent := carrier.Get("traceparent")
	tracestate := carrier.Get("tracestate")

	if len(traceparent) > maxTraceparentLen {
		return newOperationError("enqueue outbox event", errTraceparentTooLong)
	}
	if len(tracestate) > maxTracestateLen {
		return newOperationError("enqueue outbox event", errTracestateTooLong)
	}

	payload := event.Payload
	if payload == nil {
		// pgx encodes a nil []byte as SQL NULL. The durable outbox contract
		// allows a zero-byte payload while the schema requires BYTEA NOT NULL,
		// so normalize nil to an explicit empty byte slice at the DB boundary.
		payload = []byte{}
	}

	if _, err := db.Exec(
		ctx,
		enqueueSQL,
		event.ID,
		event.Subject,
		payload,
		traceparent,
		tracestate,
	); err != nil {
		return newOperationError("enqueue outbox event", err)
	}
	return nil
}
