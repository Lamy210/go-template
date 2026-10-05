package integration

import (
	"context"
	"testing"
)

func TestOutboxRetryScheduledConstraintRejectsImpossibleStates(t *testing.T) {
	pool, ctx := openTestPool(t)

	const prefix = "retry-marker-constraint-"
	t.Cleanup(func() {
		_, _ = pool.Exec(
			context.Background(),
			"DELETE FROM outbox_events WHERE event_id LIKE $1",
			prefix+"%",
		)
	})

	tests := []struct {
		name string
		sql  string
	}{
		{
			name: "never claimed",
			sql: `INSERT INTO outbox_events
			      (event_id, subject, payload, retry_scheduled)
			      VALUES ($1, 'example.retry-marker', ''::bytea, TRUE)`,
		},
		{
			name: "still leased",
			sql: `INSERT INTO outbox_events
			      (event_id, subject, payload, attempts, retry_scheduled, locked_until, lock_token)
			      VALUES ($1, 'example.retry-marker', ''::bytea, 1, TRUE,
			              CURRENT_TIMESTAMP + INTERVAL '1 minute', 'token')`,
		},
		{
			name: "already published",
			sql: `INSERT INTO outbox_events
			      (event_id, subject, payload, attempts, retry_scheduled, published_at)
			      VALUES ($1, 'example.retry-marker', ''::bytea, 1, TRUE, CURRENT_TIMESTAMP)`,
		},
		{
			name: "already failed",
			sql: `INSERT INTO outbox_events
			      (event_id, subject, payload, attempts, retry_scheduled, failed_at)
			      VALUES ($1, 'example.retry-marker', ''::bytea, 1, TRUE, CURRENT_TIMESTAMP)`,
		},
	}

	for i, tt := range tests {
		if _, err := pool.Exec(ctx, tt.sql, prefix+string(rune('a'+i))); err == nil {
			t.Fatalf("insert with impossible retry_scheduled state %q succeeded", tt.name)
		}
	}

	const validID = prefix + "valid"
	if _, err := pool.Exec(
		ctx,
		`INSERT INTO outbox_events
		 (event_id, subject, payload, attempts, retry_scheduled)
		 VALUES ($1, 'example.retry-marker', ''::bytea, 1, TRUE)`,
		validID,
	); err != nil {
		t.Fatalf("insert valid scheduled retry state: %v", err)
	}
}
