package integration

import (
	"context"
	"testing"
)

func TestOutboxStatePartitionRejectsImpossibleStates(t *testing.T) {
	pool, ctx := openTestPool(t)

	const prefix = "state-partition-constraint-"
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
			name: "published without any claim",
			sql: `INSERT INTO outbox_events
			      (event_id, subject, payload, published_at)
			      VALUES ($1, 'example.state-partition', ''::bytea, CURRENT_TIMESTAMP)`,
		},
		{
			name: "failed without any claim",
			sql: `INSERT INTO outbox_events
			      (event_id, subject, payload, failed_at)
			      VALUES ($1, 'example.state-partition', ''::bytea, CURRENT_TIMESTAMP)`,
		},
	}

	for i, tt := range tests {
		if _, err := pool.Exec(ctx, tt.sql, prefix+string(rune('a'+i))); err == nil {
			t.Fatalf("insert with impossible outbox state %q succeeded", tt.name)
		}
	}

	valid := []struct {
		name string
		sql  string
	}{
		{
			name: "fresh pending",
			sql: `INSERT INTO outbox_events
			      (event_id, subject, payload)
			      VALUES ($1, 'example.state-partition', ''::bytea)`,
		},
		{
			name: "legacy unlocked retry-compatible state",
			sql: `INSERT INTO outbox_events
			      (event_id, subject, payload, attempts)
			      VALUES ($1, 'example.state-partition', ''::bytea, 1)`,
		},
		{
			name: "scheduled retry",
			sql: `INSERT INTO outbox_events
			      (event_id, subject, payload, attempts, retry_scheduled)
			      VALUES ($1, 'example.state-partition', ''::bytea, 1, TRUE)`,
		},
		{
			name: "published after claim",
			sql: `INSERT INTO outbox_events
			      (event_id, subject, payload, attempts, published_at)
			      VALUES ($1, 'example.state-partition', ''::bytea, 1, CURRENT_TIMESTAMP)`,
		},
		{
			name: "failed after claim",
			sql: `INSERT INTO outbox_events
			      (event_id, subject, payload, attempts, failed_at)
			      VALUES ($1, 'example.state-partition', ''::bytea, 1, CURRENT_TIMESTAMP)`,
		},
	}

	for i, tt := range valid {
		if _, err := pool.Exec(ctx, tt.sql, prefix+"valid-"+string(rune('a'+i))); err != nil {
			t.Fatalf("insert valid outbox state %q: %v", tt.name, err)
		}
	}
}
