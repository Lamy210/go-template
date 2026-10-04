package integration

import (
	"math"
	"testing"
	"time"

	"github.com/Lamy210/go-template/internal/platform/outbox"
)

func TestOutboxClaimTerminalizesPostgresIntegerMaxWithoutOverflow(t *testing.T) {
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
	if len(claimed) != 0 {
		t.Fatalf("claimed events = %d, want physical-max row terminalized without publish", len(claimed))
	}

	var (
		durableAttempts int64
		failed          bool
		locked          bool
	)
	if err := pool.QueryRow(
		ctx,
		`SELECT attempts,
		        failed_at IS NOT NULL,
		        locked_until IS NOT NULL OR lock_token IS NOT NULL
		 FROM outbox_events
		 WHERE event_id = $1`,
		eventID,
	).Scan(&durableAttempts, &failed, &locked); err != nil {
		t.Fatalf("read physical-max recovery state: %v", err)
	}
	if durableAttempts != math.MaxInt32 {
		t.Fatalf("durable attempts = %d, want saturated %d", durableAttempts, math.MaxInt32)
	}
	if !failed {
		t.Fatal("physical-max recovery row was not terminally failed")
	}
	if locked {
		t.Fatal("physical-max recovery row retained a lease")
	}
}
