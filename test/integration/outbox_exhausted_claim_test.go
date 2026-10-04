package integration

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Lamy210/go-template/internal/platform/outbox"
)

func TestOutboxDispatcherRecoversExpiredFinalAttemptWithoutRepublish(t *testing.T) {
	pool, ctx := openTestPool(t)

	const eventID = "expired-final-attempt"
	if _, err := pool.Exec(ctx, "DELETE FROM outbox_events WHERE event_id = $1", eventID); err != nil {
		t.Fatalf("clear outbox event: %v", err)
	}
	if err := outbox.Enqueue(
		ctx,
		pool,
		outbox.Event{
			ID:      eventID,
			Subject: "example.expired-final-attempt",
			Payload: []byte("payload"),
		},
		nil,
	); err != nil {
		t.Fatalf("enqueue outbox event: %v", err)
	}

	store, err := outbox.NewStore(pool)
	if err != nil {
		t.Fatalf("new outbox store: %v", err)
	}

	claimed, err := store.Claim(ctx, outbox.ClaimConfig{
		BatchSize: 1,
		Lease:     time.Minute,
	})
	if err != nil {
		t.Fatalf("claim final attempt: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed events = %d, want 1", len(claimed))
	}
	if claimed[0].Attempts != 1 {
		t.Fatalf("claimed attempts = %d, want 1", claimed[0].Attempts)
	}

	// Simulate a process crash after the final allowed claim but before publish
	// or settlement. The durable row keeps attempts=1 while its lease expires.
	if _, err := pool.Exec(
		ctx,
		`UPDATE outbox_events
		 SET locked_until = CURRENT_TIMESTAMP - INTERVAL '1 second'
		 WHERE event_id = $1`,
		eventID,
	); err != nil {
		t.Fatalf("expire claimed lease: %v", err)
	}

	var publishCalls atomic.Int32
	dispatcher, err := outbox.NewDispatcher(
		store,
		func(context.Context, string, string, []byte) error {
			publishCalls.Add(1)
			return nil
		},
		nil,
		outbox.DispatcherConfig{
			BatchSize:      1,
			PollInterval:   10 * time.Millisecond,
			Lease:          time.Second,
			MaxAttempts:    1,
			RetryBaseDelay: 10 * time.Millisecond,
			RetryMaxDelay:  20 * time.Millisecond,
			PublishTimeout: 100 * time.Millisecond,
			StoreTimeout:   100 * time.Millisecond,
		},
	)
	if err != nil {
		t.Fatalf("new outbox dispatcher: %v", err)
	}

	dispatchCtx, cancelDispatcher := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() {
		runErr <- dispatcher.Run(dispatchCtx)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		var (
			failed   bool
			attempts int
		)
		if err := pool.QueryRow(
			ctx,
			`SELECT failed_at IS NOT NULL, attempts
			 FROM outbox_events
			 WHERE event_id = $1`,
			eventID,
		).Scan(&failed, &attempts); err != nil {
			cancelDispatcher()
			t.Fatalf("read recovered outbox state: %v", err)
		}
		if failed {
			if attempts != 2 {
				cancelDispatcher()
				t.Fatalf("outbox attempts = %d, want recovery claim attempt 2", attempts)
			}
			break
		}

		select {
		case err := <-runErr:
			cancelDispatcher()
			t.Fatalf("dispatcher stopped before terminal recovery: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			cancelDispatcher()
			t.Fatal("expired final attempt was not terminally failed")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if got := publishCalls.Load(); got != 0 {
		cancelDispatcher()
		t.Fatalf("publish calls = %d, want 0 after exhausted recovery claim", got)
	}

	cancelDispatcher()
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("dispatcher shutdown: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("dispatcher did not stop after cancellation")
	}
}
