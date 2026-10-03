package config

import (
	"testing"
	"time"
)

func TestNATSValidateRejectsDuplicateWindowBelowServerMinimum(t *testing.T) {
	t.Parallel()

	cfg := defaultNATSConfig()
	cfg.Enabled = true
	cfg.DuplicateWindow = 99 * time.Millisecond
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want duplicate-window minimum error")
	}

	cfg.DuplicateWindow = 100 * time.Millisecond
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() at 100ms server minimum = %v", err)
	}
}
