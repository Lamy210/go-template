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
	if got := err.Error(); got != "dispatch outbox claimed batch" {
		t.Fatalf("dispatchBatch() error text = %q, want sanitized operation", got)
	}
	if got := publishCalls.Load(); got != 0 {
		t.Fatalf("publish calls = %d, want 0", got)
	}
}
