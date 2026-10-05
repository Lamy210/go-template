package integration

import (
	"context"
	"testing"
)

func TestOutboxTraceContextConstraintRequiresParentForState(t *testing.T) {
	pool, ctx := openTestPool(t)

	const prefix = "trace-context-constraint-"
	t.Cleanup(func() {
		_, _ = pool.Exec(
			context.Background(),
			"DELETE FROM outbox_events WHERE event_id LIKE $1",
			prefix+"%",
		)
	})

	if _, err := pool.Exec(
		ctx,
		`INSERT INTO outbox_events
		 (event_id, subject, payload, tracestate)
		 VALUES ($1, 'example.trace-context', ''::bytea, 'vendor=value')`,
		prefix+"missing-parent",
	); err == nil {
		t.Fatal("insert tracestate without traceparent succeeded")
	}

	valid := []struct {
		id          string
		traceparent string
		tracestate  string
	}{
		{
			id: prefix + "empty",
		},
		{
			id:          prefix + "parent-only",
			traceparent: "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01",
		},
		{
			id:          prefix + "parent-and-state",
			traceparent: "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01",
			tracestate:  "vendor=value",
		},
	}

	for _, tt := range valid {
		if _, err := pool.Exec(
			ctx,
			`INSERT INTO outbox_events
			 (event_id, subject, payload, traceparent, tracestate)
			 VALUES ($1, 'example.trace-context', ''::bytea, $2, $3)`,
			tt.id,
			tt.traceparent,
			tt.tracestate,
		); err != nil {
			t.Fatalf("insert valid trace context state %q: %v", tt.id, err)
		}
	}
}
