package config

import (
	"testing"
	"time"
)

func TestOutboxValidateRejectsRetryBasePrecisionLoss(t *testing.T) {
	t.Parallel()

	cfg := defaultOutboxConfig()
	cfg.Enabled = true
	cfg.RetryBaseDelay = time.Second + time.Nanosecond
	cfg.RetryMaxDelay = 2 * time.Second

	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want retry base precision error")
	}
}
