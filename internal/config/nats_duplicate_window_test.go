package config

import (
	"testing"
	"time"
)

func TestNATSValidateRejectsDuplicateWindowLongerThanMaxAge(t *testing.T) {
	t.Parallel()

	cfg := defaultNATSConfig()
	cfg.Enabled = true
	cfg.MaxAge = time.Minute
	cfg.DuplicateWindow = 2 * time.Minute

	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want duplicate-window/max-age error")
	}
}

func TestNATSValidateAllowsDuplicateWindowEqualToMaxAge(t *testing.T) {
	t.Parallel()

	cfg := defaultNATSConfig()
	cfg.Enabled = true
	cfg.MaxAge = time.Minute
	cfg.DuplicateWindow = time.Minute

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want equality accepted", err)
	}
}
