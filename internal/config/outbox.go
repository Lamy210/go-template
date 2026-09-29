package config

import (
	"fmt"
	"time"
)

const (
	defaultOutboxEnabled         = false
	defaultOutboxBatchSize       = 8
	defaultOutboxPollInterval    = 500 * time.Millisecond
	defaultOutboxLease           = 30 * time.Second
	defaultOutboxMaxAttempts     = 10
	defaultOutboxRetryBase       = time.Second
	defaultOutboxRetryMax        = time.Minute
	defaultOutboxPublishTimeout  = 5 * time.Second
	defaultOutboxStoreTimeout    = 2 * time.Second
	defaultOutboxShutdownTimeout = 10 * time.Second
)

// OutboxConfig controls the optional transactional-outbox dispatcher.
// Durable enqueue/storage remains available whenever PostgreSQL is enabled,
// regardless of this runtime dispatcher flag.
type OutboxConfig struct {
	Enabled         bool
	BatchSize       int
	PollInterval    time.Duration
	Lease           time.Duration
	MaxAttempts     int
	RetryBaseDelay  time.Duration
	RetryMaxDelay   time.Duration
	PublishTimeout  time.Duration
	StoreTimeout    time.Duration
	ShutdownTimeout time.Duration
}

func loadOutbox(lookup lookupEnv) (OutboxConfig, error) {
	enabled, err := boolValue(
		lookup,
		"OUTBOX_DISPATCH_ENABLED",
		defaultOutboxEnabled,
	)
	if err != nil {
		return OutboxConfig{}, err
	}

	cfg := defaultOutboxConfig()
	cfg.Enabled = enabled
	if !enabled {
		return cfg, nil
	}

	batchSize, err := intValue(
		lookup,
		"OUTBOX_DISPATCH_BATCH_SIZE",
		defaultOutboxBatchSize,
	)
	if err != nil {
		return OutboxConfig{}, err
	}
	pollInterval, err := durationValue(
		lookup,
		"OUTBOX_DISPATCH_POLL_INTERVAL",
		defaultOutboxPollInterval,
	)
	if err != nil {
		return OutboxConfig{}, err
	}
	lease, err := durationValue(
		lookup,
		"OUTBOX_DISPATCH_LEASE",
		defaultOutboxLease,
	)
	if err != nil {
		return OutboxConfig{}, err
	}
	maxAttempts, err := intValue(
		lookup,
		"OUTBOX_DISPATCH_MAX_ATTEMPTS",
		defaultOutboxMaxAttempts,
	)
	if err != nil {
		return OutboxConfig{}, err
	}
	retryBase, err := durationValue(
		lookup,
		"OUTBOX_DISPATCH_RETRY_BASE_DELAY",
		defaultOutboxRetryBase,
	)
	if err != nil {
		return OutboxConfig{}, err
	}
	retryMax, err := durationValue(
		lookup,
		"OUTBOX_DISPATCH_RETRY_MAX_DELAY",
		defaultOutboxRetryMax,
	)
	if err != nil {
		return OutboxConfig{}, err
	}
	publishTimeout, err := durationValue(
		lookup,
		"OUTBOX_DISPATCH_PUBLISH_TIMEOUT",
		defaultOutboxPublishTimeout,
	)
	if err != nil {
		return OutboxConfig{}, err
	}
	storeTimeout, err := durationValue(
		lookup,
		"OUTBOX_DISPATCH_STORE_TIMEOUT",
		defaultOutboxStoreTimeout,
	)
	if err != nil {
		return OutboxConfig{}, err
	}
	shutdownTimeout, err := durationValue(
		lookup,
		"OUTBOX_DISPATCH_SHUTDOWN_TIMEOUT",
		defaultOutboxShutdownTimeout,
	)
	if err != nil {
		return OutboxConfig{}, err
	}

	cfg.BatchSize = batchSize
	cfg.PollInterval = pollInterval
	cfg.Lease = lease
	cfg.MaxAttempts = maxAttempts
	cfg.RetryBaseDelay = retryBase
	cfg.RetryMaxDelay = retryMax
	cfg.PublishTimeout = publishTimeout
	cfg.StoreTimeout = storeTimeout
	cfg.ShutdownTimeout = shutdownTimeout

	if err := cfg.Validate(); err != nil {
		return OutboxConfig{}, err
	}
	return cfg, nil
}

func defaultOutboxConfig() OutboxConfig {
	return OutboxConfig{
		Enabled:         defaultOutboxEnabled,
		BatchSize:       defaultOutboxBatchSize,
		PollInterval:    defaultOutboxPollInterval,
		Lease:           defaultOutboxLease,
		MaxAttempts:     defaultOutboxMaxAttempts,
		RetryBaseDelay:  defaultOutboxRetryBase,
		RetryMaxDelay:   defaultOutboxRetryMax,
		PublishTimeout:  defaultOutboxPublishTimeout,
		StoreTimeout:    defaultOutboxStoreTimeout,
		ShutdownTimeout: defaultOutboxShutdownTimeout,
	}
}

// Validate rejects unbounded dispatcher behavior.
func (c OutboxConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	if c.BatchSize <= 0 || c.BatchSize > 256 {
		return fmt.Errorf("OUTBOX_DISPATCH_BATCH_SIZE must be between 1 and 256")
	}
	if c.PollInterval <= 0 {
		return fmt.Errorf("OUTBOX_DISPATCH_POLL_INTERVAL must be positive")
	}
	if c.Lease < time.Microsecond || c.Lease > 24*time.Hour {
		return fmt.Errorf("OUTBOX_DISPATCH_LEASE must be between one microsecond and 24h")
	}
	if c.MaxAttempts <= 0 {
		return fmt.Errorf("OUTBOX_DISPATCH_MAX_ATTEMPTS must be positive")
	}
	if c.RetryBaseDelay < time.Microsecond || c.RetryMaxDelay < time.Microsecond {
		return fmt.Errorf("outbox dispatcher retry delays must be at least one microsecond")
	}
	if c.RetryMaxDelay < c.RetryBaseDelay {
		return fmt.Errorf(
			"OUTBOX_DISPATCH_RETRY_MAX_DELAY must not be less than OUTBOX_DISPATCH_RETRY_BASE_DELAY",
		)
	}
	if c.RetryMaxDelay > 24*time.Hour {
		return fmt.Errorf("OUTBOX_DISPATCH_RETRY_MAX_DELAY must not exceed 24h")
	}
	if c.PublishTimeout <= 0 {
		return fmt.Errorf("OUTBOX_DISPATCH_PUBLISH_TIMEOUT must be positive")
	}
	if c.StoreTimeout <= 0 {
		return fmt.Errorf("OUTBOX_DISPATCH_STORE_TIMEOUT must be positive")
	}
	if c.Lease <= c.PublishTimeout ||
		c.StoreTimeout >= c.Lease-c.PublishTimeout {
		return fmt.Errorf(
			"OUTBOX_DISPATCH_LEASE must exceed publish plus store timeouts",
		)
	}
	if c.ShutdownTimeout <= c.PublishTimeout ||
		c.StoreTimeout >= c.ShutdownTimeout-c.PublishTimeout {
		return fmt.Errorf(
			"OUTBOX_DISPATCH_SHUTDOWN_TIMEOUT must exceed publish plus store timeouts",
		)
	}
	return nil
}
