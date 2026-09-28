package config

import (
	"testing"
	"time"
)

func TestOutboxDisabledIgnoresInvalidDispatcherSettings(t *testing.T) {
	t.Parallel()

	values := map[string]string{
		"OUTBOX_DISPATCH_ENABLED":          "false",
		"OUTBOX_DISPATCH_BATCH_SIZE":       "invalid",
		"OUTBOX_DISPATCH_POLL_INTERVAL":    "invalid",
		"OUTBOX_DISPATCH_PUBLISH_TIMEOUT":  "invalid",
		"OUTBOX_DISPATCH_STORE_TIMEOUT":    "invalid",
		"OUTBOX_DISPATCH_SHUTDOWN_TIMEOUT": "invalid",
	}
	cfg, err := loadOutbox(func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	})
	if err != nil {
		t.Fatalf("loadOutbox() error = %v", err)
	}
	if cfg.Enabled {
		t.Fatal("Outbox.Enabled = true, want false")
	}
}

func TestOutboxEnabledLoadsBounds(t *testing.T) {
	t.Parallel()

	values := map[string]string{
		"OUTBOX_DISPATCH_ENABLED":          "true",
		"OUTBOX_DISPATCH_BATCH_SIZE":       "50",
		"OUTBOX_DISPATCH_POLL_INTERVAL":    "250ms",
		"OUTBOX_DISPATCH_LEASE":            "20s",
		"OUTBOX_DISPATCH_MAX_ATTEMPTS":     "7",
		"OUTBOX_DISPATCH_RETRY_BASE_DELAY": "500ms",
		"OUTBOX_DISPATCH_RETRY_MAX_DELAY":  "20s",
		"OUTBOX_DISPATCH_PUBLISH_TIMEOUT":  "4s",
		"OUTBOX_DISPATCH_STORE_TIMEOUT":    "2s",
		"OUTBOX_DISPATCH_SHUTDOWN_TIMEOUT": "8s",
	}
	cfg, err := loadOutbox(func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	})
	if err != nil {
		t.Fatalf("loadOutbox() error = %v", err)
	}
	if cfg.BatchSize != 50 || cfg.MaxAttempts != 7 {
		t.Fatalf("unexpected outbox config = %+v", cfg)
	}
	if cfg.PollInterval != 250*time.Millisecond ||
		cfg.PublishTimeout != 4*time.Second ||
		cfg.StoreTimeout != 2*time.Second {
		t.Fatalf("unexpected outbox durations = %+v", cfg)
	}
}

func TestOutboxValidateRejectsUnsafeBounds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*OutboxConfig)
	}{
		{
			name:   "batch too large",
			mutate: func(cfg *OutboxConfig) { cfg.BatchSize = 257 },
		},
		{
			name: "lease not longer than publish plus store",
			mutate: func(cfg *OutboxConfig) {
				cfg.Lease = cfg.PublishTimeout + cfg.StoreTimeout
			},
		},
		{
			name: "retry max below base",
			mutate: func(cfg *OutboxConfig) {
				cfg.RetryMaxDelay = cfg.RetryBaseDelay / 2
			},
		},
		{
			name:   "zero attempts",
			mutate: func(cfg *OutboxConfig) { cfg.MaxAttempts = 0 },
		},
		{
			name: "shutdown budget too small",
			mutate: func(cfg *OutboxConfig) {
				cfg.ShutdownTimeout = cfg.PublishTimeout + cfg.StoreTimeout
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := defaultOutboxConfig()
			cfg.Enabled = true
			tt.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("Validate() error = nil, want error")
			}
		})
	}
}

func TestConfigRejectsOutboxDispatcherWithoutDatabaseAndNATS(t *testing.T) {
	t.Parallel()

	_, err := load(func(key string) (string, bool) {
		values := map[string]string{
			"OUTBOX_DISPATCH_ENABLED": "true",
		}
		value, ok := values[key]
		return value, ok
	})
	if err == nil {
		t.Fatal("load() error = nil, want dependency error")
	}
}

func TestConfigAcceptsOutboxDispatcherWithDatabaseAndNATS(t *testing.T) {
	t.Parallel()

	values := map[string]string{
		"DATABASE_ENABLED":        "true",
		"DATABASE_URL":            "postgres://example.invalid/app",
		"NATS_ENABLED":            "true",
		"OUTBOX_DISPATCH_ENABLED": "true",
	}
	cfg, err := load(func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	})
	if err != nil {
		t.Fatalf("load() error = %v", err)
	}
	if !cfg.Outbox.Enabled || !cfg.Database.Enabled || !cfg.NATS.Enabled {
		t.Fatalf("unexpected profile state: %+v", cfg)
	}
}
