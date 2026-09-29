package outbox

import (
	"context"

	"github.com/Lamy210/go-template/internal/core/safeerror"
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
		return safeerror.Wrap("enqueue outbox event", errNilDBTX)
	}
	if err := event.Validate(); err != nil {
		return err
	}

	carrier := newPropagationCarrier()
	if propagator != nil {
		propagator.Inject(ctx, carrier)
	}
	traceparent := carrier.Get("traceparent")
	tracestate := carrier.Get("tracestate")

	if len(traceparent) > maxTraceparentLen {
		return safeerror.Wrap("enqueue outbox event", errTraceparentTooLong)
	}
	if len(tracestate) > maxTracestateLen {
		return safeerror.Wrap("enqueue outbox event", errTracestateTooLong)
	}

	if _, err := db.Exec(
		ctx,
		enqueueSQL,
		event.ID,
		event.Subject,
		event.Payload,
		traceparent,
		tracestate,
	); err != nil {
		return safeerror.Wrap("enqueue outbox event", err)
	}
	return nil
}
