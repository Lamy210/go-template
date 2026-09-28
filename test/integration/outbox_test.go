package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Lamy210/go-template/internal/platform/database"
	"github.com/Lamy210/go-template/internal/platform/outbox"
	"github.com/jackc/pgx/v5"
)

func TestTransactionalOutboxLifecycle(t *testing.T) {
	pool, ctx := openTestPool(t)

	if _, err := pool.Exec(ctx, "DELETE FROM outbox_events"); err != nil {
		t.Fatalf("clear outbox: %v", err)
	}

	t.Run("rollback removes event atomically", func(t *testing.T) {
		sentinel := errors.New("rollback outbox test")
		err := database.InTx(ctx, pool, func(tx pgx.Tx) error {
			if err := outbox.Enqueue(
				ctx,
				tx,
				outbox.Event{
					ID:      "rollback-event",
					Subject: "example.rollback",
					Payload: []byte("rollback"),
				},
				nil,
			); err != nil {
				return err
			}
			return sentinel
		})
		if !errors.Is(err, sentinel) {
			t.Fatalf("database.InTx() error = %v, want sentinel", err)
		}

		var count int
		if err := pool.QueryRow(
			ctx,
			"SELECT count(*) FROM outbox_events WHERE event_id = $1",
			"rollback-event",
		).Scan(&count); err != nil {
			t.Fatalf("count rolled-back event: %v", err)
		}
		if count != 0 {
			t.Fatalf("rolled-back outbox rows = %d, want 0", count)
		}
	})

	if err := outbox.Enqueue(
		ctx,
		pool,
		outbox.Event{
			ID:      "lifecycle-event",
			Subject: "example.lifecycle",
			Payload: []byte("payload"),
		},
		nil,
	); err != nil {
		t.Fatalf("enqueue lifecycle event: %v", err)
	}

	store, err := outbox.NewStore(pool)
	if err != nil {
		t.Fatalf("new outbox store: %v", err)
	}

	claimCfg := outbox.ClaimConfig{
		BatchSize: 10,
		Lease:     time.Second,
	}
	claimed, err := store.Claim(ctx, claimCfg)
	if err != nil {
		t.Fatalf("claim outbox event: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed events = %d, want 1", len(claimed))
	}
	first := claimed[0]
	if first.EventID != "lifecycle-event" {
		t.Fatalf("claimed event ID = %q", first.EventID)
	}
	if first.Attempts != 1 {
		t.Fatalf("claimed attempts = %d, want 1", first.Attempts)
	}
	if first.LockToken == "" {
		t.Fatal("claim lock token is empty")
	}

	locked, err := store.Claim(ctx, claimCfg)
	if err != nil {
		t.Fatalf("claim while leased: %v", err)
	}
	if len(locked) != 0 {
		t.Fatalf("claim while leased returned %d events, want 0", len(locked))
	}

	stale := first
	stale.LockToken = "not-the-owner"
	if err := store.MarkPublished(ctx, stale); err == nil {
		t.Fatal("MarkPublished() with stale token succeeded")
	}

	if err := store.Retry(ctx, first, 30*time.Millisecond); err != nil {
		t.Fatalf("schedule retry: %v", err)
	}
	notReady, err := store.Claim(ctx, claimCfg)
	if err != nil {
		t.Fatalf("claim before retry delay: %v", err)
	}
	if len(notReady) != 0 {
		t.Fatalf("claim before retry delay returned %d events, want 0", len(notReady))
	}

	time.Sleep(50 * time.Millisecond)
	reclaimed, err := store.Claim(ctx, claimCfg)
	if err != nil {
		t.Fatalf("claim retried event: %v", err)
	}
	if len(reclaimed) != 1 {
		t.Fatalf("reclaimed events = %d, want 1", len(reclaimed))
	}
	second := reclaimed[0]
	if second.Attempts != 2 {
		t.Fatalf("reclaimed attempts = %d, want 2", second.Attempts)
	}
	if second.LockToken == first.LockToken {
		t.Fatal("reclaimed event reused old lock token")
	}

	if err := store.MarkPublished(ctx, second); err != nil {
		t.Fatalf("mark published: %v", err)
	}
	afterPublish, err := store.Claim(ctx, claimCfg)
	if err != nil {
		t.Fatalf("claim after publish: %v", err)
	}
	if len(afterPublish) != 0 {
		t.Fatalf("claim after publish returned %d events, want 0", len(afterPublish))
	}

	var published bool
	if err := pool.QueryRow(
		ctx,
		"SELECT published_at IS NOT NULL FROM outbox_events WHERE event_id = $1",
		"lifecycle-event",
	).Scan(&published); err != nil {
		t.Fatalf("read published state: %v", err)
	}
	if !published {
		t.Fatal("published_at was not set")
	}
}

func TestOutboxDuplicateEventIDIsSanitized(t *testing.T) {
	pool, ctx := openTestPool(t)

	const eventID = "duplicate-event-id"
	if _, err := pool.Exec(ctx, "DELETE FROM outbox_events WHERE event_id = $1", eventID); err != nil {
		t.Fatalf("clear duplicate event: %v", err)
	}
	event := outbox.Event{
		ID:      eventID,
		Subject: "example.duplicate",
		Payload: []byte("payload"),
	}
	if err := outbox.Enqueue(ctx, pool, event, nil); err != nil {
		t.Fatalf("first enqueue: %v", err)
	}
	err := outbox.Enqueue(ctx, pool, event, nil)
	if err == nil {
		t.Fatal("duplicate enqueue succeeded")
	}
	if got := err.Error(); got != "enqueue outbox event" {
		t.Fatalf("duplicate enqueue error text = %q", got)
	}
}
