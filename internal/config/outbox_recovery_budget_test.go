package config

import (
	"math"
	"testing"
)

func TestOutboxConfigReservesAttemptForCrashRecovery(t *testing.T) {
	t.Parallel()

	cfg := defaultOutboxConfig()
	cfg.Enabled = true
	cfg.MaxAttempts = math.MaxInt32 - 1
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() with one recovery attempt of headroom = %v", err)
	}

	cfg.MaxAttempts = math.MaxInt32
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() error = nil at PostgreSQL INTEGER max without recovery headroom")
	}
}
