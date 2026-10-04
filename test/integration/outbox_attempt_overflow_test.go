package integration

import (
	"math"
	"testing"
	"time"

	"github.com/Lamy210/go-template/internal/platform/outbox"
)

func TestOutboxClaimRecoversPostgresIntegerMaxWithoutOverflow(t *testing.T) {
	pool, ctx := openTestPool(t)

	const eventID = "integer-max-recovery"
	if _, err := pool.Exec(ctx, "DELETE FROM outbox_events WHERE event_id = $1", eventID); err != nil {
		t.Fatalf("clear outbox event: %v", err)
	}
	if err := outbox.Enqueue(
		ctx,
		pool,
		outbox.Event{
			ID:      eventID,
			Subject: "example.integer-max-recovery",
			Payload: []byte("payload"),
		},
		nil,
	); err != nil {
		t.Fatalf("enqueue outbox event: %v", err)
	}

	if _, err := pool.Exec(
		ctx,
		`UPDATE outbox_events
		 SET attempts = $2,
		     available_at = CURRENT_TIMESTAMP,
		     locked_until = NULL,
		     lock_token = NULL
		 WHERE event_id = $1`,
		eventID,
		math.MaxInt32,
	); err != nil {
		t.Fatalf("seed PostgreSQL INTEGER max attempts: %v", err)
	}

	store, err := outbox.NewStore(pool)
	if err != nil {
		t.Fatalf("new outbox store: %v", err)
	}
	claimed, err := store.Claim(ctx, outbox.ClaimConfig{
		BatchSize: 1,
		Lease:     time.Second,
	})
	if err != nil {
		t.Fatalf("claim integer-max recovery row: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed events = %d, want 1", len(claimed))
	}
	wantLogicalAttempt := int64(math.MaxInt32) + 1
	if got := int64(claimed[0].Attempts); got != wantLogicalAttempt {
		t.Fatalf("logical recovery attempt = %d, want %d", got, wantLogicalAttempt)
	}

	var durableAttempts int64
	if err := pool.QueryRow(
		ctx,
		"SELECT attempts FROM outbox_events WHERE event_id = $1",
		eventID,
	).Scan(&durableAttempts); err != nil {
		t.Fatalf("read durable attempts: %v", err)
	}
	if durableAttempts != math.MaxInt32 {
		t.Fatalf("durable attempts = %d, want saturated %d", durableAttempts, math.MaxInt32)
	}

	if err := store.MarkFailed(ctx, claimed[0]); err != nil {
		t.Fatalf("mark saturated recovery claim failed: %v", err)
	}
	var failed bool
	if err := pool.QueryRow(
		ctx,
		"SELECT failed_at IS NOT NULL FROM outbox_events WHERE event_id = $1",
		eventID,
	).Scan(&failed); err != nil {
		t.Fatalf("read terminal recovery state: %v", err)
	}
	if !failed {
		t.Fatal("saturated recovery claim was not terminally failed")
	}
}
