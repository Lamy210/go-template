package integration

import (
	"context"
	"testing"
)

func TestOutboxLeaseConstraintRejectsImpossibleStates(t *testing.T) {
	pool, ctx := openTestPool(t)

	const prefix = "lease-state-constraint-"
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
			name: "never claimed but leased",
			sql: `INSERT INTO outbox_events
			      (event_id, subject, payload, locked_until, lock_token)
			      VALUES ($1, 'example.lease-state', ''::bytea,
			              CURRENT_TIMESTAMP + INTERVAL '1 minute', 'token')`,
		},
		{
			name: "empty lease token",
			sql: `INSERT INTO outbox_events
			      (event_id, subject, payload, attempts, locked_until, lock_token)
			      VALUES ($1, 'example.lease-state', ''::bytea, 1,
			              CURRENT_TIMESTAMP + INTERVAL '1 minute', '')`,
		},
		{
			name: "published while leased",
			sql: `INSERT INTO outbox_events
			      (event_id, subject, payload, attempts, locked_until, lock_token, published_at)
			      VALUES ($1, 'example.lease-state', ''::bytea, 1,
			              CURRENT_TIMESTAMP + INTERVAL '1 minute', 'token', CURRENT_TIMESTAMP)`,
		},
		{
			name: "failed while leased",
			sql: `INSERT INTO outbox_events
			      (event_id, subject, payload, attempts, locked_until, lock_token, failed_at)
			      VALUES ($1, 'example.lease-state', ''::bytea, 1,
			              CURRENT_TIMESTAMP + INTERVAL '1 minute', 'token', CURRENT_TIMESTAMP)`,
		},
	}

	for i, tt := range tests {
		if _, err := pool.Exec(ctx, tt.sql, prefix+string(rune('a'+i))); err == nil {
			t.Fatalf("insert with impossible lease state %q succeeded", tt.name)
		}
	}

	const validID = prefix + "valid"
	if _, err := pool.Exec(
		ctx,
		`INSERT INTO outbox_events
		 (event_id, subject, payload, attempts, locked_until, lock_token)
		 VALUES ($1, 'example.lease-state', ''::bytea, 1,
		         CURRENT_TIMESTAMP + INTERVAL '1 minute', 'token')`,
		validID,
	); err != nil {
		t.Fatalf("insert valid active lease state: %v", err)
	}
}
