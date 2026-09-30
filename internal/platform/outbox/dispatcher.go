package outbox

import (
	"context"
	"encoding/binary"
	"errors"
	"hash/fnv"
	"time"

	coreprop "github.com/Lamy210/go-template/internal/core/propagation"
	"github.com/Lamy210/go-template/internal/outboxbudget"
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

var (
	errPublisherPanic = errors.New("outbox publisher panicked")
	// ErrPermanentPublishFailure marks a publisher rejection that cannot become
	// successful by retrying the same durable event unchanged.
	ErrPermanentPublishFailure = errors.New("outbox publish permanently rejected")
)

type permanentPublishFailure struct {
	cause error
}

func (e *permanentPublishFailure) Error() string {
	return ErrPermanentPublishFailure.Error()
}

func (e *permanentPublishFailure) Unwrap() error {
	return e.cause
}

func (e *permanentPublishFailure) Is(target error) bool {
	return target == ErrPermanentPublishFailure
}

// MarkPermanentPublishFailure lets a composition adapter classify a
// transport-specific rejection without making the outbox import that transport.
func MarkPermanentPublishFailure(cause error) error {
	if cause == nil {
		return nil
	}
	return &permanentPublishFailure{cause: cause}
}

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
	if c.RetryBaseDelay < time.Microsecond || c.RetryMaxDelay < time.Microsecond {
		return errors.New("outbox dispatcher retry delays must be at least one microsecond")
	}
	if c.RetryMaxDelay < c.RetryBaseDelay || c.RetryMaxDelay > maxClaimLease {
		return errors.New("outbox dispatcher retry max delay is outside allowed bounds")
	}
	if c.PublishTimeout <= 0 || c.StoreTimeout <= 0 {
		return errors.New("outbox dispatcher publish and store timeouts must be positive")
	}
	if !outboxbudget.LeaseCoversClaimPublishSettlement(
		c.Lease,
		c.PublishTimeout,
		c.StoreTimeout,
	) {
		return errors.New(
			"outbox dispatcher lease must exceed claim store plus publish plus settlement store timeouts",
		)
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
			// dispatchBatch only returns storage/settlement failures. Those
			// errors represent uncertain durable state and must remain visible
			// even when shutdown cancellation happened concurrently.
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
	batchCtx, cancelBatch := context.WithCancel(ctx)
	defer cancelBatch()

	results := make(chan error, len(events))
	for _, event := range events {
		go func(event ClaimedEvent) {
			results <- d.dispatchOne(batchCtx, event)
		}(event)
	}

	var fatalErrs []error
	for range events {
		err := <-results
		if err == nil {
			continue
		}
		if len(fatalErrs) == 0 {
			// dispatchOne only returns fatal publisher/storage/settlement
			// failures. Stop sibling broker work immediately, but keep draining
			// every result so each canceled claim can attempt bounded settlement.
			cancelBatch()
		}
		fatalErrs = append(fatalErrs, err)
	}
	return errors.Join(fatalErrs...)
}

func (d *Dispatcher) dispatchOne(ctx context.Context, event ClaimedEvent) error {
	publishCtx := d.restoreContext(ctx, event)
	publishCtx, cancelPublish := context.WithTimeout(publishCtx, d.cfg.PublishTimeout)
	err := invokePublisher(
		d.publisher,
		publishCtx,
		event.Subject,
		event.EventID,
		event.Payload,
	)
	cancelPublish()
	if errors.Is(err, errPublisherPanic) {
		// A publisher panic is a programming/infrastructure failure, not a
		// normal broker rejection. Do not mutate durable state: leave the
		// lease to expire and stop the dispatcher with a sanitized error.
		return newOperationError("publish outbox event", err)
	}

	settleBase := context.WithoutCancel(ctx)
	settleCtx, cancelSettle := context.WithTimeout(settleBase, d.cfg.StoreTimeout)
	defer cancelSettle()

	if err == nil {
		if settleErr := d.store.MarkPublished(settleCtx, event); settleErr != nil {
			return newOperationError("mark outbox event published", settleErr)
		}
		return nil
	}

	if errors.Is(err, ErrPermanentPublishFailure) {
		// The publisher contract guarantees the unchanged event cannot be
		// accepted. Settle it immediately instead of consuming the full retry
		// budget. This check precedes cancellation ambiguity because permanent
		// classification specifically guarantees no successful publish.
		if failErr := d.store.MarkFailed(settleCtx, event); failErr != nil {
			return newOperationError("mark permanently rejected outbox event failed", failErr)
		}
		return nil
	}

	// Cancellation makes ordinary publish success ambiguous. Release the lease
	// for a later at-least-once retry rather than permanently failing the event.
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

	delay := retryDelayForEvent(
		event.EventID,
		event.Attempts,
		d.cfg.RetryBaseDelay,
		d.cfg.RetryMaxDelay,
	)
	if retryErr := d.store.Retry(settleCtx, event, delay); retryErr != nil {
		return newOperationError("schedule outbox retry", retryErr)
	}
	return nil
}

func invokePublisher(
	publisher Publisher,
	ctx context.Context,
	subject string,
	eventID string,
	payload []byte,
) (err error) {
	defer func() {
		if recover() != nil {
			err = errPublisherPanic
		}
	}()
	return publisher(ctx, subject, eventID, payload)
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

func retryDelayForEvent(
	eventID string,
	attempt int,
	base time.Duration,
	maximum time.Duration,
) time.Duration {
	envelope := retryDelay(attempt, base, maximum)
	if attempt <= 1 || eventID == "" {
		return envelope
	}

	envelopeMicros := envelope / time.Microsecond
	if envelopeMicros <= 1 {
		return envelope
	}

	// Equal-ish deterministic jitter in the final 25% of the exponential
	// envelope. Keeping the upper bound at the existing envelope preserves the
	// configured maximum while spreading events even after exponential backoff
	// reaches its cap.
	windowMicros := envelopeMicros / 4
	if windowMicros == 0 {
		return envelope
	}
	lowerMicros := envelopeMicros - windowMicros
	slotCount := uint64(windowMicros) + 1
	offsetMicros := retryJitterHash(eventID, attempt) % slotCount

	return time.Duration(uint64(lowerMicros)+offsetMicros) * time.Microsecond
}

func retryJitterHash(eventID string, attempt int) uint64 {
	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte(eventID))

	var attemptBytes [8]byte
	binary.LittleEndian.PutUint64(attemptBytes[:], uint64(attempt))
	_, _ = hasher.Write(attemptBytes[:])
	return hasher.Sum64()
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
