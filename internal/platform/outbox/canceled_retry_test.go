package outbox

import (
	"context"
	"sync/atomic"
	"testing"
)

func TestDispatcherPublishesScheduledRetryPastAttemptLimit(t *testing.T) {
	t.Parallel()

	store := &fakeEventStore{}
	var publishCalls atomic.Int32
	dispatcher := mustDispatcher(
		t,
		store,
		func(context.Context, string, string, []byte) error {
			publishCalls.Add(1)
			return nil
		},
		nil,
	)

	event := ClaimedEvent{
		ID:             1,
		EventID:        "event-1",
		Subject:        "example.created",
		Attempts:       dispatcher.cfg.MaxAttempts + 1,
		LockToken:      "token",
		RetryScheduled: true,
	}
	if err := dispatcher.dispatchBatch(context.Background(), []ClaimedEvent{event}); err != nil {
		t.Fatalf("dispatchBatch() error = %v", err)
	}
	if got := publishCalls.Load(); got != 1 {
		t.Fatalf("publish calls = %d, want 1", got)
	}
	if len(store.published) != 1 {
		t.Fatalf("published settlements = %d, want 1", len(store.published))
	}
	if len(store.failed) != 0 {
		t.Fatalf("failed settlements = %d, want 0", len(store.failed))
	}
}
