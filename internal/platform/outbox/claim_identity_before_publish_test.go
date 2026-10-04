package outbox

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

func TestDispatcherRejectsInvalidClaimIdentityBeforePublish(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*ClaimedEvent)
	}{
		{
			name: "non-positive row ID",
			mutate: func(event *ClaimedEvent) {
				event.ID = 0
			},
		},
		{
			name: "missing lock token",
			mutate: func(event *ClaimedEvent) {
				event.LockToken = ""
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeEventStore{}
			var settlementCalls atomic.Int32
			store.markPublishedFn = func(context.Context, ClaimedEvent) error {
				settlementCalls.Add(1)
				return errors.New("settlement should not be reached")
			}

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
				ID:        1,
				EventID:   "event-1",
				Subject:   "example.created",
				Attempts:  1,
				LockToken: "token-1",
			}
			tt.mutate(&event)

			if err := dispatcher.dispatchOne(context.Background(), event); err == nil {
				t.Fatal("dispatchOne() error = nil, want invalid claim identity error")
			}
			if got := publishCalls.Load(); got != 0 {
				t.Fatalf("publish calls = %d, want 0", got)
			}
			if got := settlementCalls.Load(); got != 0 {
				t.Fatalf("settlement calls = %d, want 0", got)
			}
		})
	}
}
