package outbox

import (
	"math"
	"testing"
	"time"
)

func TestDispatcherConfigReservesAttemptForCrashRecovery(t *testing.T) {
	t.Parallel()

	cfg := DispatcherConfig{
		BatchSize:      1,
		PollInterval:   time.Second,
		Lease:          10 * time.Second,
		MaxAttempts:    math.MaxInt32 - 1,
		RetryBaseDelay: time.Second,
		RetryMaxDelay:  time.Second,
		PublishTimeout: time.Second,
		StoreTimeout:   time.Second,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() with one recovery attempt of headroom = %v", err)
	}

	cfg.MaxAttempts = math.MaxInt32
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() error = nil at PostgreSQL INTEGER max without recovery headroom")
	}
}
