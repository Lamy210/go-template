package outbox

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestDispatcherConfigRejectsRetryBasePrecisionLoss(t *testing.T) {
	t.Parallel()

	cfg := DispatcherConfig{
		BatchSize:      1,
		PollInterval:   time.Second,
		Lease:          10 * time.Second,
		MaxAttempts:    1,
		RetryBaseDelay: time.Second + time.Nanosecond,
		RetryMaxDelay:  2 * time.Second,
		PublishTimeout: time.Second,
		StoreTimeout:   time.Second,
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want retry base precision error")
	}
}

func TestStoreRetryRejectsRetryDelayPrecisionLoss(t *testing.T) {
	t.Parallel()

	store := &Store{}
	err := store.Retry(
		context.Background(),
		ClaimedEvent{ID: 1, LockToken: "token"},
		time.Microsecond+time.Nanosecond,
	)
	if err == nil {
		t.Fatal("Retry() error = nil, want retry delay precision error")
	}
	if !strings.Contains(err.Error(), "whole microseconds") {
		t.Fatalf("Retry() error = %q, want whole-microseconds validation", err.Error())
	}
}
