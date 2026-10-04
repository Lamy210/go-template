package outbox

import (
	"context"
	"sync/atomic"
	"testing"
)

func TestDispatcherRejectsClaimBatchAboveConfiguredLimit(t *testing.T) {
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
	dispatcher.cfg.BatchSize = 1

	err := dispatcher.dispatchBatch(
		context.Background(),
		[]ClaimedEvent{
			{
				ID:        1,
				EventID:   "event-1",
				Subject:   "example.created",
				Attempts:  1,
				LockToken: "token-1",
			},
			{
				ID:        2,
				EventID:   "event-2",
				Subject:   "example.created",
				Attempts:  1,
				LockToken: "token-2",
			},
		},
	)
	if err == nil {
		t.Fatal("dispatchBatch() error = nil, want oversized batch error")
	}
	if got := err.Error(); got != "dispatch outbox claimed batch" {
		t.Fatalf("dispatchBatch() error text = %q, want sanitized operation", got)
	}
	if got := publishCalls.Load(); got != 0 {
		t.Fatalf("publish calls = %d, want 0", got)
	}
}

func TestDispatcherRejectsDuplicateClaimedRowBeforePublish(t *testing.T) {
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

	err := dispatcher.dispatchBatch(
		context.Background(),
		[]ClaimedEvent{
			{
				ID:        1,
				EventID:   "event-1",
				Subject:   "example.created",
				Attempts:  1,
				LockToken: "token",
			},
			{
				ID:        1,
				EventID:   "event-1-duplicate",
				Subject:   "example.created",
				Attempts:  1,
				LockToken: "token",
			},
		},
	)
	if err == nil {
		t.Fatal("dispatchBatch() error = nil, want duplicate claim error")
	}
	if got := err.Error(); got != "dispatch outbox claimed batch" {
		t.Fatalf("dispatchBatch() error text = %q, want sanitized operation", got)
	}
	if got := publishCalls.Load(); got != 0 {
		t.Fatalf("publish calls = %d, want 0", got)
	}
}

func TestDispatcherRejectsDuplicateEventIDBeforePublish(t *testing.T) {
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

	err := dispatcher.dispatchBatch(
		context.Background(),
		[]ClaimedEvent{
			{
				ID:        1,
				EventID:   "event-1",
				Subject:   "example.created",
				Payload:   []byte("first"),
				Attempts:  1,
				LockToken: "token",
			},
			{
				ID:        2,
				EventID:   "event-1",
				Subject:   "example.created",
				Payload:   []byte("second"),
				Attempts:  1,
				LockToken: "token",
			},
		},
	)
	if err == nil {
		t.Fatal("dispatchBatch() error = nil, want duplicate event ID error")
	}
	if got := err.Error(); got != "dispatch outbox claimed batch" {
		t.Fatalf("dispatchBatch() error text = %q, want sanitized operation", got)
	}
	if got := publishCalls.Load(); got != 0 {
		t.Fatalf("publish calls = %d, want 0", got)
	}
}

func TestDispatcherRejectsNonPositiveClaimAttemptsBeforePublish(t *testing.T) {
	t.Parallel()

	for _, attempts := range []int{-1, 0} {
		attempts := attempts
		t.Run("attempts", func(t *testing.T) {
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

			err := dispatcher.dispatchBatch(
				context.Background(),
				[]ClaimedEvent{{
					ID:        1,
					EventID:   "event-1",
					Subject:   "example.created",
					Attempts:  attempts,
					LockToken: "token",
				}},
			)
			if err == nil {
				t.Fatal("dispatchBatch() error = nil, want invalid attempt count error")
			}
			if got := err.Error(); got != "dispatch outbox claimed batch" {
				t.Fatalf("dispatchBatch() error text = %q, want sanitized operation", got)
			}
			if got := publishCalls.Load(); got != 0 {
				t.Fatalf("publish calls = %d, want 0", got)
			}
		})
	}
}

func TestDispatcherTerminalizesExhaustedClaimWithoutPublish(t *testing.T) {
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
	if dispatcher.cfg.MaxAttempts != 5 {
		t.Fatalf("test requires MaxAttempts=5, got %d", dispatcher.cfg.MaxAttempts)
	}

	event := ClaimedEvent{
		ID:        1,
		EventID:   "event-1",
		Subject:   "example.created",
		Attempts:  dispatcher.cfg.MaxAttempts + 1,
		LockToken: "token",
	}
	if err := dispatcher.dispatchBatch(context.Background(), []ClaimedEvent{event}); err != nil {
		t.Fatalf("dispatchBatch() error = %v", err)
	}
	if got := publishCalls.Load(); got != 0 {
		t.Fatalf("publish calls = %d, want 0", got)
	}
	if len(store.failed) != 1 {
		t.Fatalf("failed settlements = %d, want 1", len(store.failed))
	}
	if store.failed[0].ID != event.ID || store.failed[0].Attempts != event.Attempts {
		t.Fatalf("failed settlement = %+v, want recovery claim %+v", store.failed[0], event)
	}
}

func TestDispatcherRejectsInvalidClaimedEventContentBeforePublish(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*ClaimedEvent)
	}{
		{
			name: "empty event ID",
			mutate: func(event *ClaimedEvent) {
				event.EventID = ""
			},
		},
		{
			name: "blank subject",
			mutate: func(event *ClaimedEvent) {
				event.Subject = "   "
			},
		},
		{
			name: "oversized payload",
			mutate: func(event *ClaimedEvent) {
				event.Payload = make([]byte, maxPayloadBytes+1)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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

			invalid := ClaimedEvent{
				ID:        1,
				EventID:   "event-1",
				Subject:   "example.created",
				Payload:   []byte("payload"),
				Attempts:  1,
				LockToken: "token-1",
			}
			tt.mutate(&invalid)

			err := dispatcher.dispatchBatch(
				context.Background(),
				[]ClaimedEvent{
					invalid,
					{
						ID:        2,
						EventID:   "valid-sibling",
						Subject:   "example.created",
						Payload:   []byte("payload"),
						Attempts:  1,
						LockToken: "token-2",
					},
				},
			)
			if err == nil {
				t.Fatal("dispatchBatch() error = nil, want invalid claimed event error")
			}
			if got := err.Error(); got != "dispatch outbox claimed batch" {
				t.Fatalf("dispatchBatch() error text = %q, want sanitized operation", got)
			}
			if got := publishCalls.Load(); got != 0 {
				t.Fatalf("publish calls = %d, want 0", got)
			}
		})
	}
}

func TestDispatcherPreflightsAllClaimIdentityBeforePublishingBatch(t *testing.T) {
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

	err := dispatcher.dispatchBatch(
		context.Background(),
		[]ClaimedEvent{
			{
				ID:        1,
				EventID:   "invalid-claim",
				Subject:   "example.created",
				Attempts:  1,
				LockToken: "",
			},
			{
				ID:        2,
				EventID:   "valid-sibling",
				Subject:   "example.created",
				Attempts:  1,
				LockToken: "token-2",
			},
		},
	)
	if err == nil {
		t.Fatal("dispatchBatch() error = nil, want invalid claim batch error")
	}
	if got := publishCalls.Load(); got != 0 {
		t.Fatalf("publish calls = %d, want 0", got)
	}
	if got := err.Error(); got != "dispatch outbox claimed batch" {
		t.Fatalf("dispatchBatch() error text = %q, want sanitized operation", got)
	}
}
