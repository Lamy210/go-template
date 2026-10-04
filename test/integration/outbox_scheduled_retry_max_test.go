package integration

import (
	"math"
	"testing"
	"time"

	"github.com/Lamy210/go-template/internal/platform/outbox"
)

func TestOutboxClaimKeepsScheduledRetryClaimableAtPostgresIntegerMax(t *testing.T) {
	pool, ctx := openTestPool(t)

	const eventID = "integer-max-scheduled-retry"
	if _, err := pool.Exec(ctx, "DELETE FROM outbox_events WHERE event_id = $1", eventID); err != nil {
		t.Fatalf("clear outbox event: %v", err)
	}
	if err := outbox.Enqueue(
		ctx,
		pool,
		outbox.Event{
			ID:      eventID,
			Subject: "example.integer-max-scheduled-retry",
			Payload: []byte("payload"),
		},
		nil,
	); err != nil {
		t.Fatalf("enqueue outbox event: %v", err)
	}

	var rowID int64
	if err := pool.QueryRow(
		ctx,
		`UPDATE outbox_events
		 SET attempts = $2,
		     locked_until = CURRENT_TIMESTAMP + INTERVAL '1 minute',
		     lock_token = 'scheduled-retry-seed'
		 WHERE event_id = $1
		 RETURNING id`,
		eventID,
		math.MaxInt32,
	).Scan(&rowID); err != nil {
		t.Fatalf("seed integer-max claimed row: %v", err)
	}

	store, err := outbox.NewStore(pool)
	if err != nil {
		t.Fatalf("new outbox store: %v", err)
	}
	if err := store.Retry(
		ctx,
		outbox.ClaimedEvent{
			ID:        rowID,
			EventID:   eventID,
			Subject:   "example.integer-max-scheduled-retry",
			Attempts:  math.MaxInt32,
			LockToken: "scheduled-retry-seed",
		},
		time.Microsecond,
	); err != nil {
		t.Fatalf("schedule integer-max retry: %v", err)
	}

	if _, err := pool.Exec(
		ctx,
		"UPDATE outbox_events SET available_at = CURRENT_TIMESTAMP WHERE event_id = $1",
		eventID,
	); err != nil {
		t.Fatalf("make scheduled retry immediately available: %v", err)
	}

	claimed, err := store.Claim(ctx, outbox.ClaimConfig{
		BatchSize: 1,
		Lease:     time.Second,
	})
	if err != nil {
		t.Fatalf("claim scheduled integer-max retry: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed events = %d, want 1", len(claimed))
	}
	if claimed[0].Attempts != math.MaxInt32 {
		t.Fatalf("claimed attempts = %d, want saturated %d", claimed[0].Attempts, math.MaxInt32)
	}
	if !claimed[0].RetryScheduled {
		t.Fatal("scheduled retry provenance was not returned with claim")
	}

	if err := store.MarkFailed(ctx, claimed[0]); err != nil {
		t.Fatalf("settle scheduled integer-max retry: %v", err)
	}
}
