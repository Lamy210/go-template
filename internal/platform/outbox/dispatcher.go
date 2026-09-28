package outbox

import (
	"context"
	"errors"
	"time"

	coreprop "github.com/Lamy210/go-template/internal/core/propagation"
)

// EventStore is the storage boundary required by the dispatcher.
type EventStore interface {
	Claim(context.Context, ClaimConfig) ([]ClaimedEvent, error)
	MarkPublished(context.Context, ClaimedEvent) error
	Retry(context.Context, ClaimedEvent, time.Duration) error
	MarkFailed(context.Context, ClaimedEvent) error
}

// Publisher sends one durable event to the external broker.
type Publisher func(context.Context, string, string, []byte) error

// DispatcherConfig bounds polling, publishing, retry, and settlement behavior.
type DispatcherConfig struct {
	BatchSize      int
	PollInterval   time.Duration
	Lease          time.Duration
	MaxAttempts    int
	RetryBaseDelay time.Duration
	RetryMaxDelay  time.Duration
	PublishTimeout time.Duration
	StoreTimeout   time.Duration
}

// Validate rejects unbounded dispatcher behavior.
func (c DispatcherConfig) Validate() error {
	if c.BatchSize <= 0 || c.BatchSize > 256 {
		return errors.New("outbox dispatcher batch size must be between 1 and 256")
	}
	if c.PollInterval <= 0 {
		return errors.New("outbox dispatcher poll interval must be positive")
	}
	if c.Lease < time.Microsecond || c.Lease > maxClaimLease {
		return errors.New("outbox dispatcher lease must be between one microsecond and 24 hours")
	}
	if c.MaxAttempts <= 0 {
		return errors.New("outbox dispatcher max attempts must be positive")
	}
	if c.RetryBaseDelay <= 0 || c.RetryMaxDelay <= 0 {
		return errors.New("outbox dispatcher retry delays must be positive")
	}
	if c.RetryMaxDelay < c.RetryBaseDelay || c.RetryMaxDelay > maxClaimLease {
		return errors.New("outbox dispatcher retry max delay is outside allowed bounds")
	}
	if c.PublishTimeout <= 0 || c.StoreTimeout <= 0 {
		return errors.New("outbox dispatcher publish and store timeouts must be positive")
	}
	if c.Lease <= c.PublishTimeout+c.StoreTimeout {
		return errors.New("outbox dispatcher lease must exceed publish plus store timeouts")
	}
	return nil
}

// Dispatcher claims durable events and settles them after bounded publish calls.
type Dispatcher struct {
	store      EventStore
	publisher  Publisher
	propagator coreprop.TextMapPropagator
	cfg        DispatcherConfig
}

// NewDispatcher validates dependencies and dispatcher bounds.
func NewDispatcher(
	store EventStore,
	publisher Publisher,
	propagator coreprop.TextMapPropagator,
	cfg DispatcherConfig,
) (*Dispatcher, error) {
	if store == nil {
		return nil, errors.New("outbox dispatcher store must not be nil")
	}
	if publisher == nil {
		return nil, errors.New("outbox dispatcher publisher must not be nil")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Dispatcher{
		store:      store,
		publisher:  publisher,
		propagator: propagator,
		cfg:        cfg,
	}, nil
}

// Run blocks until ctx is canceled or a storage/settlement failure occurs.
//
// Broker publish failures are converted into finite retry or permanent-failure
// state transitions and do not crash the service.
func (d *Dispatcher) Run(ctx context.Context) error {
	for {
		if ctx.Err() != nil {
			return nil
		}

		claimCtx, cancelClaim := context.WithTimeout(ctx, d.cfg.StoreTimeout)
		events, err := d.store.Claim(claimCtx, ClaimConfig{
			BatchSize: d.cfg.BatchSize,
			Lease:     d.cfg.Lease,
		})
		cancelClaim()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return newOperationError("claim outbox dispatch batch", err)
		}

		if len(events) == 0 {
			if err := waitForPoll(ctx, d.cfg.PollInterval); err != nil {
				return nil
			}
			continue
		}

		if err := d.dispatchBatch(ctx, events); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if ctx.Err() != nil {
			return nil
		}
	}
}

func (d *Dispatcher) dispatchBatch(
	ctx context.Context,
	events []ClaimedEvent,
) error {
	results := make(chan error, len(events))
	for _, event := range events {
		go func(event ClaimedEvent) {
			results <- d.dispatchOne(ctx, event)
		}(event)
	}

	var firstErr error
	for range events {
		if err := <-results; err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (d *Dispatcher) dispatchOne(ctx context.Context, event ClaimedEvent) error {
	publishCtx := d.restoreContext(ctx, event)
	publishCtx, cancelPublish := context.WithTimeout(publishCtx, d.cfg.PublishTimeout)
	err := d.publisher(publishCtx, event.Subject, event.EventID, event.Payload)
	cancelPublish()

	settleBase := context.WithoutCancel(ctx)
	settleCtx, cancelSettle := context.WithTimeout(settleBase, d.cfg.StoreTimeout)
	defer cancelSettle()

	if err == nil {
		if settleErr := d.store.MarkPublished(settleCtx, event); settleErr != nil {
			return newOperationError("mark outbox event published", settleErr)
		}
		return nil
	}

	// Cancellation makes publish success ambiguous. Release the lease for a
	// later at-least-once retry rather than permanently failing the event.
	if ctx.Err() != nil {
		if retryErr := d.store.Retry(settleCtx, event, d.cfg.RetryBaseDelay); retryErr != nil {
			return newOperationError("release canceled outbox event", retryErr)
		}
		return nil
	}

	if event.Attempts >= d.cfg.MaxAttempts {
		if failErr := d.store.MarkFailed(settleCtx, event); failErr != nil {
			return newOperationError("mark outbox event failed", failErr)
		}
		return nil
	}

	delay := retryDelay(
		event.Attempts,
		d.cfg.RetryBaseDelay,
		d.cfg.RetryMaxDelay,
	)
	if retryErr := d.store.Retry(settleCtx, event, delay); retryErr != nil {
		return newOperationError("schedule outbox retry", retryErr)
	}
	return nil
}

func (d *Dispatcher) restoreContext(
	ctx context.Context,
	event ClaimedEvent,
) context.Context {
	if d.propagator == nil {
		return ctx
	}
	carrier := newPropagationCarrier()
	if event.Traceparent != "" {
		carrier.Set("traceparent", event.Traceparent)
	}
	if event.Tracestate != "" {
		carrier.Set("tracestate", event.Tracestate)
	}
	return d.propagator.Extract(ctx, carrier)
}

func retryDelay(attempt int, base, maximum time.Duration) time.Duration {
	if attempt <= 1 {
		return base
	}

	delay := base
	for current := 1; current < attempt; current++ {
		if delay >= maximum {
			return maximum
		}
		if delay > maximum/2 {
			return maximum
		}
		delay *= 2
	}
	if delay > maximum {
		return maximum
	}
	return delay
}

func waitForPoll(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
