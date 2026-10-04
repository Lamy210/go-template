package outbox

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"time"

	coreprop "github.com/Lamy210/go-template/internal/core/propagation"
	"github.com/Lamy210/go-template/internal/outboxbudget"
)

// ErrPermanentPublishFailure classifies a publish rejection that cannot succeed
// when the same durable event is retried unchanged. It is intentionally owned by
// the transport-neutral outbox boundary so composition adapters can map concrete
// broker/input errors without teaching the outbox package about a broker.
var ErrPermanentPublishFailure = errors.New("outbox publish permanently rejected")

var (
	errPublisherPanic  = errors.New("outbox publisher panicked")
	errEventStorePanic = errors.New("outbox event store panicked")
)

type permanentPublishError struct {
	cause error
}

func (e *permanentPublishError) Error() string {
	return ErrPermanentPublishFailure.Error()
}

func (e *permanentPublishError) Unwrap() []error {
	return []error{ErrPermanentPublishFailure, e.cause}
}

// MarkPermanentPublishFailure classifies err as a deterministic rejection of
// the unchanged durable event while retaining the original cause for
// errors.Is/errors.As traversal. The returned Error string remains sanitized.
func MarkPermanentPublishFailure(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrPermanentPublishFailure) {
		return err
	}
	return &permanentPublishError{cause: err}
}

// Publisher sends one durable event to an external transport. The event ID must
// be used as the transport deduplication key when the transport supports one.
type Publisher func(context.Context, string, string, []byte) error

// EventStore is the storage boundary required by the dispatcher.
type EventStore interface {
	Claim(context.Context, ClaimConfig) ([]ClaimedEvent, error)
	MarkPublished(context.Context, ClaimedEvent) error
	Retry(context.Context, ClaimedEvent, time.Duration) error
	MarkFailed(context.Context, ClaimedEvent) error
}

// DispatcherConfig bounds polling, leases, retries, and external calls.
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

// Validate rejects unbounded or internally inconsistent dispatcher behavior.
func (c DispatcherConfig) Validate() error {
	if c.BatchSize <= 0 || c.BatchSize > 1000 {
		return errors.New("outbox dispatcher batch size must be between 1 and 1000")
	}
	if c.PollInterval <= 0 {
		return errors.New("outbox dispatcher poll interval must be positive")
	}
	if c.Lease < time.Microsecond || c.Lease > maxClaimLease {
		return errors.New("outbox dispatcher lease must be between one microsecond and 24 hours")
	}
	if c.MaxAttempts <= 0 || int64(c.MaxAttempts) > int64(math.MaxInt32) {
		return errors.New("outbox dispatcher max attempts must be between 1 and PostgreSQL INTEGER max")
	}
	if c.RetryBaseDelay < time.Microsecond || c.RetryMaxDelay < time.Microsecond {
		return errors.New("outbox dispatcher retry delays must be at least one microsecond")
	}
	if !outboxbudget.FitsPostgresIntervalPrecision(c.RetryBaseDelay) ||
		!outboxbudget.FitsPostgresIntervalPrecision(c.RetryMaxDelay) {
		return errors.New("outbox dispatcher retry delays must use whole microseconds")
	}
	if c.RetryBaseDelay > c.RetryMaxDelay || c.RetryMaxDelay > maxClaimLease {
		return errors.New("outbox dispatcher retry delays are inconsistent")
	}
	if c.PublishTimeout <= 0 || c.StoreTimeout <= 0 {
		return errors.New("outbox dispatcher operation timeouts must be positive")
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

func (d *Dispatcher) validateInitialized() error {
	if d == nil || d.store == nil || d.publisher == nil {
		return errors.New("outbox dispatcher must be initialized")
	}
	return nil
}

func (d *Dispatcher) validateClaimForDispatch(event ClaimedEvent) error {
	if err := validateClaimIdentity(event); err != nil {
		return err
	}
	if event.Attempts <= 0 || event.Attempts > d.cfg.MaxAttempts {
		return errors.New("outbox claimed event attempts outside dispatcher bounds")
	}
	return nil
}

func validateClaimedEventContent(event ClaimedEvent) error {
	if err := (Event{
		ID:      event.EventID,
		Subject: event.Subject,
		Payload: event.Payload,
	}).Validate(); err != nil {
		return fmt.Errorf("outbox claimed event content is invalid: %w", err)
	}
	return nil
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
	if err := d.validateInitialized(); err != nil {
		return err
	}

	for {
		if ctx.Err() != nil {
			return nil
		}

		claimCtx, cancelClaim := context.WithTimeout(ctx, d.cfg.StoreTimeout)
		events, err := invokeStoreClaim(d.store, claimCtx, ClaimConfig{
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
			// dispatchBatch only returns fatal extension/storage/settlement failures.
			// Those errors represent violated runtime bounds or uncertain durable
			// state and must remain visible even when shutdown cancellation happened
			// concurrently.
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
	if len(events) > d.cfg.BatchSize {
		return newOperationError(
			"dispatch outbox claimed batch",
			errors.New("outbox event store returned more events than requested"),
		)
	}

	claimedRows := make(map[int64]struct{}, len(events))
	claimedEventIDs := make(map[string]struct{}, len(events))
	for _, event := range events {
		if err := d.validateClaimForDispatch(event); err != nil {
			return newOperationError("dispatch outbox claimed batch", err)
		}
		if err := validateClaimedEventContent(event); err != nil {
			return newOperationError("dispatch outbox claimed batch", err)
		}
		if _, exists := claimedRows[event.ID]; exists {
			return newOperationError(
				"dispatch outbox claimed batch",
				errors.New("outbox event store returned duplicate claimed row"),
			)
		}
		claimedRows[event.ID] = struct{}{}

		if _, exists := claimedEventIDs[event.EventID]; exists {
			return newOperationError(
				"dispatch outbox claimed batch",
				errors.New("outbox event store returned duplicate event ID"),
			)
		}
		claimedEventIDs[event.EventID] = struct{}{}
	}

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
	if err := d.validateClaimForDispatch(event); err != nil {
		return newOperationError("dispatch outbox claimed event", err)
	}

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
		if settleErr := invokeStoreOperation(func() error {
			return d.store.MarkPublished(settleCtx, event)
		}); settleErr != nil {
			return newOperationError("mark outbox event published", settleErr)
		}
		return nil
	}

	if errors.Is(err, ErrPermanentPublishFailure) {
		// The publisher contract guarantees the unchanged event cannot be
		// accepted. Settle it immediately instead of consuming the full retry
		// budget. This check precedes cancellation ambiguity because permanent
		// classification specifically guarantees no successful publish.
		if failErr := invokeStoreOperation(func() error {
			return d.store.MarkFailed(settleCtx, event)
		}); failErr != nil {
			return newOperationError("mark permanently rejected outbox event failed", failErr)
		}
		return nil
	}

	if ctx.Err() != nil || publishCtx.Err() != nil {
		// Cancellation can race with broker acknowledgement. The outcome is
		// ambiguous, so release the claim for retry even at MaxAttempts rather
		// than marking it permanently failed.
		if retryErr := invokeStoreOperation(func() error {
			return d.store.Retry(settleCtx, event, d.cfg.RetryBaseDelay)
		}); retryErr != nil {
			return newOperationError("release canceled outbox event", retryErr)
		}
		return nil
	}

	if event.Attempts >= d.cfg.MaxAttempts {
		if failErr := invokeStoreOperation(func() error {
			return d.store.MarkFailed(settleCtx, event)
		}); failErr != nil {
			return newOperationError("mark failed outbox event", failErr)
		}
		return nil
	}

	delay := retryDelayForEvent(
		event.EventID,
		event.Attempts,
		d.cfg.RetryBaseDelay,
		d.cfg.RetryMaxDelay,
	)
	if retryErr := invokeStoreOperation(func() error {
		return d.store.Retry(settleCtx, event, delay)
	}); retryErr != nil {
		return newOperationError("schedule outbox retry", retryErr)
	}
	return nil
}

func (d *Dispatcher) restoreContext(ctx context.Context, event ClaimedEvent) context.Context {
	if d.propagator == nil || (event.Traceparent == "" && event.Tracestate == "") {
		return ctx
	}
	carrier := newPropagationCarrier()
	carrier.Set("traceparent", event.Traceparent)
	carrier.Set("tracestate", event.Tracestate)
	return extractPropagationSafely(ctx, d.propagator, carrier)
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

func invokeStoreClaim(
	store EventStore,
	ctx context.Context,
	cfg ClaimConfig,
) (events []ClaimedEvent, err error) {
	defer func() {
		if recover() != nil {
			events = nil
			err = errEventStorePanic
		}
	}()
	return store.Claim(ctx, cfg)
}

func invokeStoreOperation(operation func() error) (err error) {
	defer func() {
		if recover() != nil {
			err = errEventStorePanic
		}
	}()
	return operation()
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

func retryDelay(attempt int, base, maxDelay time.Duration) time.Duration {
	if attempt <= 1 {
		return base
	}
	if base >= maxDelay {
		return maxDelay
	}

	delay := base
	for step := 1; step < attempt; step++ {
		if delay >= maxDelay/2 {
			return maxDelay
		}
		delay *= 2
	}
	if delay > maxDelay {
		return maxDelay
	}
	return delay
}

func retryDelayForEvent(eventID string, attempt int, base, maxDelay time.Duration) time.Duration {
	envelope := retryDelay(attempt, base, maxDelay)
	if attempt <= 1 {
		return envelope
	}
	if envelope <= base {
		return base
	}

	lowerBound := envelope - envelope/4
	if lowerBound < base {
		lowerBound = base
	}
	window := envelope - lowerBound
	if window <= 0 {
		return lowerBound
	}

	hash := fnv.New64a()
	_, _ = hash.Write([]byte(eventID))
	_, _ = fmt.Fprintf(hash, ":%d", attempt)

	const precision = time.Microsecond
	lowerMicros := lowerBound / precision
	upperMicros := envelope / precision
	if upperMicros <= lowerMicros {
		return lowerMicros * precision
	}
	spanMicros := uint64(upperMicros-lowerMicros) + 1
	jitterMicros := time.Duration(hash.Sum64() % spanMicros)
	return (lowerMicros + jitterMicros) * precision
}
