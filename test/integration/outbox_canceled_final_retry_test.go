package integration

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Lamy210/go-template/internal/platform/outbox"
)

func TestOutboxDispatcherRepublishesCanceledFinalAttempt(t *testing.T) {
	pool, ctx := openTestPool(t)
	if _, err := pool.Exec(ctx, "DELETE FROM outbox_events"); err != nil {
		t.Fatalf("clear outbox: %v", err)
	}

	store, err := outbox.NewStore(pool)
	if err != nil {
		t.Fatalf("new outbox store: %v", err)
	}
	cfg := outbox.DispatcherConfig{
		BatchSize:      1,
		PollInterval:   10 * time.Millisecond,
		Lease:          time.Second,
		MaxAttempts:    1,
		RetryBaseDelay: 10 * time.Millisecond,
		RetryMaxDelay:  10 * time.Millisecond,
		PublishTimeout: 100 * time.Millisecond,
		StoreTimeout:   100 * time.Millisecond,
	}

	const eventID = "canceled-final-attempt"
	if err := outbox.Enqueue(
		ctx,
		pool,
		outbox.Event{
			ID:      eventID,
			Subject: "example.canceled-final-attempt",
			Payload: []byte("payload"),
		},
		nil,
	); err != nil {
		t.Fatalf("enqueue outbox event: %v", err)
	}

	firstCtx, cancelFirst := context.WithCancel(context.Background())
	firstDispatcher, err := outbox.NewDispatcher(
		store,
		func(publishCtx context.Context, _, _ string, _ []byte) error {
			cancelFirst()
			<-publishCtx.Done()
			return publishCtx.Err()
		},
		nil,
		cfg,
	)
	if err != nil {
		t.Fatalf("new first dispatcher: %v", err)
	}
	if err := firstDispatcher.Run(firstCtx); err != nil {
		t.Fatalf("first dispatcher run: %v", err)
	}

	var (
		afterCancelAttempts int
		afterCancelFailed   bool
		afterCancelLocked   bool
	)
	if err := pool.QueryRow(
		ctx,
		`SELECT attempts,
		        failed_at IS NOT NULL,
		        locked_until IS NOT NULL OR lock_token IS NOT NULL
		 FROM outbox_events
		 WHERE event_id = $1`,
		eventID,
	).Scan(&afterCancelAttempts, &afterCancelFailed, &afterCancelLocked); err != nil {
		t.Fatalf("read canceled settlement: %v", err)
	}
	if afterCancelAttempts != 1 {
		t.Fatalf("attempts after canceled publish = %d, want 1", afterCancelAttempts)
	}
	if afterCancelFailed {
		t.Fatal("canceled final attempt was marked failed instead of scheduled for retry")
	}
	if afterCancelLocked {
		t.Fatal("canceled final attempt retained its lease after retry scheduling")
	}

	var retryPublishes atomic.Int32
	secondDispatcher, err := outbox.NewDispatcher(
		store,
		func(context.Context, string, string, []byte) error {
			retryPublishes.Add(1)
			return nil
		},
		nil,
		cfg,
	)
	if err != nil {
		t.Fatalf("new second dispatcher: %v", err)
	}
	secondCtx, cancelSecond := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancelSecond()
	if err := secondDispatcher.Run(secondCtx); err != nil {
		t.Fatalf("second dispatcher run: %v", err)
	}

	var (
		published bool
		failed    bool
		attempts  int
	)
	if err := pool.QueryRow(
		ctx,
		`SELECT published_at IS NOT NULL, failed_at IS NOT NULL, attempts
		 FROM outbox_events
		 WHERE event_id = $1`,
		eventID,
	).Scan(&published, &failed, &attempts); err != nil {
		t.Fatalf("read final settlement: %v", err)
	}
	if got := retryPublishes.Load(); got != 1 {
		t.Fatalf("retry publishes = %d, want 1 after explicit cancellation retry", got)
	}
	if !published {
		t.Fatal("explicitly retried final attempt was not published")
	}
	if failed {
		t.Fatal("explicitly retried final attempt was marked failed")
	}
	if attempts <= cfg.MaxAttempts {
		t.Fatalf("attempts after explicit retry = %d, want over-budget recovery claim", attempts)
	}
}
