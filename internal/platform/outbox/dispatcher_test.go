package outbox

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	coreprop "github.com/Lamy210/go-template/internal/core/propagation"
)

type fakeEventStore struct {
	mu              sync.Mutex
	claims          [][]ClaimedEvent
	claimErr        error
	claimFn         func(context.Context, ClaimConfig) ([]ClaimedEvent, error)
	published       []ClaimedEvent
	retried         []retryCall
	failed          []ClaimedEvent
	settleErr       error
	markPublishedFn func(context.Context, ClaimedEvent) error
}

type retryCall struct {
	event ClaimedEvent
	delay time.Duration
}

func (s *fakeEventStore) Claim(ctx context.Context, cfg ClaimConfig) ([]ClaimedEvent, error) {
	if s.claimFn != nil {
		return s.claimFn(ctx, cfg)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.claimErr != nil {
		return nil, s.claimErr
	}
	if len(s.claims) == 0 {
		return nil, nil
	}
	out := s.claims[0]
	s.claims = s.claims[1:]
	return out, nil
}

func (s *fakeEventStore) MarkPublished(ctx context.Context, event ClaimedEvent) error {
	if s.markPublishedFn != nil {
		return s.markPublishedFn(ctx, event)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.settleErr != nil {
		return s.settleErr
	}
	s.published = append(s.published, event)
	return nil
}

func (s *fakeEventStore) Retry(_ context.Context, event ClaimedEvent, delay time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.settleErr != nil {
		return s.settleErr
	}
	s.retried = append(s.retried, retryCall{event: event, delay: delay})
	return nil
}

func (s *fakeEventStore) MarkFailed(_ context.Context, event ClaimedEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.settleErr != nil {
		return s.settleErr
	}
	s.failed = append(s.failed, event)
	return nil
}

type dispatchContextKey struct{}

type dispatchPropagator struct{}

func (dispatchPropagator) Inject(context.Context, coreprop.TextMapCarrier) {}

func (dispatchPropagator) Extract(
	ctx context.Context,
	carrier coreprop.TextMapCarrier,
) context.Context {
	return context.WithValue(ctx, dispatchContextKey{}, carrier.Get("traceparent"))
}

func TestDispatcherMarksSuccessfulPublish(t *testing.T) {
	t.Parallel()

	store := &fakeEventStore{}
	event := ClaimedEvent{
		ID:        1,
		EventID:   "event-1",
		Subject:   "example.created",
		Payload:   []byte("payload"),
		Attempts:  1,
		LockToken: "token",
	}
	var publishedContext string
	dispatcher := mustDispatcher(t, store, func(ctx context.Context, subject, eventID string, payload []byte) error {
		if subject != event.Subject || eventID != event.EventID || string(payload) != "payload" {
			t.Fatalf("unexpected publish args: %q %q %q", subject, eventID, payload)
		}
		publishedContext, _ = ctx.Value(dispatchContextKey{}).(string)
		return nil
	}, dispatchPropagator{})

	event.Traceparent = "trace-context"
	if err := dispatcher.dispatchOne(context.Background(), event); err != nil {
		t.Fatalf("dispatchOne() error = %v", err)
	}
	if publishedContext != "trace-context" {
		t.Fatalf("restored trace context = %q", publishedContext)
	}
	if len(store.published) != 1 {
		t.Fatalf("published settlements = %d, want 1", len(store.published))
	}
}

type panicExtractPropagator struct{}

func (panicExtractPropagator) Inject(context.Context, coreprop.TextMapCarrier) {}

func (panicExtractPropagator) Extract(context.Context, coreprop.TextMapCarrier) context.Context {
	panic("sensitive propagation extract panic")
}

func TestDispatcherContainsPropagationExtractPanic(t *testing.T) {
	t.Parallel()

	type baseContextKey struct{}
	ctx := context.WithValue(context.Background(), baseContextKey{}, "base")
	store := &fakeEventStore{}
	var gotContext string
	dispatcher := mustDispatcher(
		t,
		store,
		func(ctx context.Context, _, _ string, _ []byte) error {
			gotContext, _ = ctx.Value(baseContextKey{}).(string)
			return nil
		},
		panicExtractPropagator{},
	)

	event := ClaimedEvent{
		ID:          1,
		EventID:     "event-1",
		Subject:     "example.created",
		Traceparent: "trace-context",
		Attempts:    1,
		LockToken:   "token",
	}
	if err := dispatcher.dispatchOne(ctx, event); err != nil {
		t.Fatalf("dispatchOne() error = %v", err)
	}
	if gotContext != "base" {
		t.Fatalf("publisher context value = %q, want base context fallback", gotContext)
	}
	if len(store.published) != 1 {
		t.Fatalf("published settlements = %d, want 1", len(store.published))
	}
}

func TestDispatcherStartsClaimedBatchConcurrently(t *testing.T) {
	t.Parallel()

	store := &fakeEventStore{}
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	dispatcher := mustDispatcher(t, store, func(context.Context, string, string, []byte) error {
		started <- struct{}{}
		<-release
		return nil
	}, nil)

	done := make(chan error, 1)
	go func() {
		done <- dispatcher.dispatchBatch(context.Background(), []ClaimedEvent{
			{ID: 1, EventID: "event-1", Subject: "example.1", LockToken: "token-1"},
			{ID: 2, EventID: "event-2", Subject: "example.2", LockToken: "token-2"},
		})
	}()

	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			close(release)
			t.Fatal("claimed batch was not started concurrently")
		}
	}
	close(release)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("dispatchBatch() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("dispatchBatch() did not finish")
	}
	if len(store.published) != 2 {
		t.Fatalf("published settlements = %d, want 2", len(store.published))
	}
}

func TestDispatcherCancelsSiblingPublishAfterFatalSettlementFailure(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("database unavailable")
	secondStarted := make(chan struct{})
	secondCanceled := make(chan struct{})

	store := &fakeEventStore{}
	store.markPublishedFn = func(_ context.Context, event ClaimedEvent) error {
		if event.ID == 1 {
			return sentinel
		}
		store.mu.Lock()
		defer store.mu.Unlock()
		store.published = append(store.published, event)
		return nil
	}

	dispatcher := mustDispatcher(
		t,
		store,
		func(ctx context.Context, _ string, eventID string, _ []byte) error {
			switch eventID {
			case "event-1":
				<-secondStarted
				return nil
			case "event-2":
				close(secondStarted)
				<-ctx.Done()
				close(secondCanceled)
				return ctx.Err()
			default:
				t.Fatalf("unexpected event ID %q", eventID)
				return nil
			}
		},
		nil,
	)

	err := dispatcher.dispatchBatch(context.Background(), []ClaimedEvent{
		{
			ID:        1,
			EventID:   "event-1",
			Subject:   "example.1",
			Attempts:  1,
			LockToken: "token-1",
		},
		{
			ID:        2,
			EventID:   "event-2",
			Subject:   "example.2",
			Attempts:  1,
			LockToken: "token-2",
		},
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("dispatchBatch() error = %v, want settlement failure", err)
	}

	select {
	case <-secondCanceled:
	case <-time.After(time.Second):
		t.Fatal("sibling publisher did not observe batch cancellation")
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.retried) != 1 || store.retried[0].event.EventID != "event-2" {
		t.Fatalf("canceled sibling retries = %#v, want event-2 retry", store.retried)
	}
	if len(store.failed) != 0 {
		t.Fatalf("canceled sibling failed settlements = %d, want 0", len(store.failed))
	}
}

func TestDispatcherContainsStoreClaimPanicAsFatalError(t *testing.T) {
	t.Parallel()

	const sensitive = "sensitive store claim panic"
	store := &fakeEventStore{
		claimFn: func(context.Context, ClaimConfig) ([]ClaimedEvent, error) {
			panic(sensitive)
		},
	}
	dispatcher := mustDispatcher(
		t,
		store,
		func(context.Context, string, string, []byte) error { return nil },
		nil,
	)

	err := dispatcher.Run(context.Background())
	if !errors.Is(err, errEventStorePanic) {
		t.Fatalf("Run() error = %v, want event store panic sentinel", err)
	}
	if got := err.Error(); got != "claim outbox dispatch batch" {
		t.Fatalf("Run() error text = %q", got)
	}
	if strings.Contains(err.Error(), sensitive) {
		t.Fatalf("Run() exposed store panic value: %q", err.Error())
	}
}

func TestDispatcherContainsStoreSettlementPanicAsFatalError(t *testing.T) {
	t.Parallel()

	const sensitive = "sensitive store settlement panic"
	store := &fakeEventStore{
		markPublishedFn: func(context.Context, ClaimedEvent) error {
			panic(sensitive)
		},
	}
	dispatcher := mustDispatcher(
		t,
		store,
		func(context.Context, string, string, []byte) error { return nil },
		nil,
	)

	err := dispatcher.dispatchOne(
		context.Background(),
		ClaimedEvent{
			ID:        1,
			EventID:   "event-1",
			Subject:   "example.created",
			Attempts:  1,
			LockToken: "token",
		},
	)
	if !errors.Is(err, errEventStorePanic) {
		t.Fatalf("dispatchOne() error = %v, want event store panic sentinel", err)
	}
	if got := err.Error(); got != "mark outbox event published" {
		t.Fatalf("dispatchOne() error text = %q", got)
	}
	if strings.Contains(err.Error(), sensitive) {
		t.Fatalf("dispatchOne() exposed store panic value: %q", err.Error())
	}
}

func TestDispatcherContainsPublisherPanicAsFatalError(t *testing.T) {
	t.Parallel()

	store := &fakeEventStore{
		claims: [][]ClaimedEvent{{
			{
				ID:        1,
				EventID:   "event-1",
				Subject:   "example.created",
				Attempts:  1,
				LockToken: "token",
			},
		}},
	}
	dispatcher := mustDispatcher(
		t,
		store,
		func(context.Context, string, string, []byte) error {
			panic("sensitive publisher panic value")
		},
		nil,
	)

	err := dispatcher.Run(context.Background())
	if !errors.Is(err, errPublisherPanic) {
		t.Fatalf("Run() error = %v, want publisher panic sentinel", err)
	}
	if got := err.Error(); got != "publish outbox event" {
		t.Fatalf("Run() error text = %q, want sanitized operation", got)
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.published) != 0 || len(store.retried) != 0 || len(store.failed) != 0 {
		t.Fatalf(
			"publisher panic mutated durable state: published=%d retried=%d failed=%d",
			len(store.published),
			len(store.retried),
			len(store.failed),
		)
	}
}

func TestPermanentPublishFailureRetainsCauseWithoutExposingIt(t *testing.T) {
	t.Parallel()

	cause := errors.New("sensitive broker rejection")
	err := MarkPermanentPublishFailure(cause)
	if !errors.Is(err, ErrPermanentPublishFailure) {
		t.Fatal("permanent publish failure sentinel is not retained")
	}
	if !errors.Is(err, cause) {
		t.Fatal("permanent publish failure cause is not retained")
	}
	if got := err.Error(); got != ErrPermanentPublishFailure.Error() {
		t.Fatalf("Error() = %q, want sanitized permanent failure text", got)
	}
}

func TestDispatcherImmediatelyFailsPermanentPublishRejection(t *testing.T) {
	t.Parallel()

	cause := errors.New("deterministic rejection")
	store := &fakeEventStore{}
	dispatcher := mustDispatcher(
		t,
		store,
		func(context.Context, string, string, []byte) error {
			return MarkPermanentPublishFailure(cause)
		},
		nil,
	)

	event := ClaimedEvent{
		ID:        1,
		EventID:   "event-1",
		Subject:   "example.created",
		Attempts:  1,
		LockToken: "token",
	}
	if err := dispatcher.dispatchOne(context.Background(), event); err != nil {
		t.Fatalf("dispatchOne() error = %v", err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.failed) != 1 {
		t.Fatalf("failed settlements = %d, want 1", len(store.failed))
	}
	if len(store.retried) != 0 {
		t.Fatalf("retry settlements = %d, want 0", len(store.retried))
	}
	if len(store.published) != 0 {
		t.Fatalf("published settlements = %d, want 0", len(store.published))
	}
}

func TestDispatcherPermanentPublishSettlementFailureIsFatal(t *testing.T) {
	t.Parallel()

	settlementErr := errors.New("database unavailable")
	store := &fakeEventStore{settleErr: settlementErr}
	dispatcher := mustDispatcher(
		t,
		store,
		func(context.Context, string, string, []byte) error {
			return MarkPermanentPublishFailure(errors.New("deterministic rejection"))
		},
		nil,
	)

	err := dispatcher.dispatchOne(
		context.Background(),
		ClaimedEvent{
			ID:        1,
			EventID:   "event-1",
			Subject:   "example.created",
			Attempts:  1,
			LockToken: "token",
		},
	)
	if !errors.Is(err, settlementErr) {
		t.Fatalf("dispatchOne() error = %v, want settlement failure", err)
	}
	if got := err.Error(); got != "mark permanently rejected outbox event failed" {
		t.Fatalf("dispatchOne() error text = %q", got)
	}
}

func TestDispatcherSchedulesCappedRetry(t *testing.T) {
	t.Parallel()

	store := &fakeEventStore{}
	dispatcher := mustDispatcher(t, store, func(context.Context, string, string, []byte) error {
		return errors.New("broker unavailable")
	}, nil)
	dispatcher.cfg.RetryBaseDelay = time.Second
	dispatcher.cfg.RetryMaxDelay = 3 * time.Second

	event := ClaimedEvent{
		ID:        1,
		EventID:   "event-1",
		Attempts:  3,
		LockToken: "token",
	}
	if err := dispatcher.dispatchOne(context.Background(), event); err != nil {
		t.Fatalf("dispatchOne() error = %v", err)
	}
	if len(store.retried) != 1 {
		t.Fatalf("retry settlements = %d, want 1", len(store.retried))
	}
	want := retryDelayForEvent(
		event.EventID,
		event.Attempts,
		dispatcher.cfg.RetryBaseDelay,
		dispatcher.cfg.RetryMaxDelay,
	)
	if got := store.retried[0].delay; got != want {
		t.Fatalf("retry delay = %v, want %v", got, want)
	}
	if want < 2250*time.Millisecond || want > 3*time.Second {
		t.Fatalf("jittered capped retry delay = %v, want 2.25s..3s", want)
	}
}

func TestDispatcherMarksFailedAtAttemptLimit(t *testing.T) {
	t.Parallel()

	store := &fakeEventStore{}
	dispatcher := mustDispatcher(t, store, func(context.Context, string, string, []byte) error {
		return errors.New("broker unavailable")
	}, nil)
	event := ClaimedEvent{
		ID:        1,
		Attempts:  dispatcher.cfg.MaxAttempts,
		LockToken: "token",
	}
	if err := dispatcher.dispatchOne(context.Background(), event); err != nil {
		t.Fatalf("dispatchOne() error = %v", err)
	}
	if len(store.failed) != 1 {
		t.Fatalf("failed settlements = %d, want 1", len(store.failed))
	}
}

func TestDispatcherCancellationReleasesLeaseForRetry(t *testing.T) {
	t.Parallel()

	store := &fakeEventStore{}
	ctx, cancel := context.WithCancel(context.Background())
	dispatcher := mustDispatcher(t, store, func(context.Context, string, string, []byte) error {
		cancel()
		return context.Canceled
	}, nil)

	event := ClaimedEvent{ID: 1, Attempts: dispatcher.cfg.MaxAttempts, LockToken: "token"}
	if err := dispatcher.dispatchOne(ctx, event); err != nil {
		t.Fatalf("dispatchOne() error = %v", err)
	}
	if len(store.retried) != 1 {
		t.Fatalf("retry settlements = %d, want 1", len(store.retried))
	}
	if len(store.failed) != 0 {
		t.Fatalf("failed settlements = %d, want 0", len(store.failed))
	}
}

func TestDispatcherCancellationDoesNotHideSettlementFailure(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("database unavailable")
	store := &fakeEventStore{
		claims: [][]ClaimedEvent{{
			{
				ID:        1,
				EventID:   "event-1",
				Subject:   "example.created",
				Attempts:  1,
				LockToken: "token",
			},
		}},
		settleErr: sentinel,
	}
	ctx, cancel := context.WithCancel(context.Background())
	dispatcher := mustDispatcher(t, store, func(context.Context, string, string, []byte) error {
		cancel()
		return context.Canceled
	}, nil)

	err := dispatcher.Run(ctx)
	if !errors.Is(err, sentinel) {
		t.Fatalf("Run() error = %v, want settlement failure", err)
	}
	if got := err.Error(); got != "release canceled outbox event" {
		t.Fatalf("Run() error text = %q", got)
	}
}

func TestDispatcherConfigRejectsSubMicrosecondRetryDelay(t *testing.T) {
	t.Parallel()

	cfg := DispatcherConfig{
		BatchSize:      1,
		PollInterval:   time.Second,
		Lease:          10 * time.Second,
		MaxAttempts:    1,
		RetryBaseDelay: 500 * time.Nanosecond,
		RetryMaxDelay:  time.Second,
		PublishTimeout: time.Second,
		StoreTimeout:   time.Second,
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want storage-precision error")
	}
}

func TestDispatcherConfigRejectsLeaseWithoutClaimBudget(t *testing.T) {
	t.Parallel()

	cfg := DispatcherConfig{
		BatchSize:      1,
		PollInterval:   time.Second,
		Lease:          2*time.Second + time.Nanosecond,
		MaxAttempts:    1,
		RetryBaseDelay: time.Second,
		RetryMaxDelay:  time.Second,
		PublishTimeout: time.Second,
		StoreTimeout:   time.Second,
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want claim+publish+settlement budget error")
	}
}

func TestDispatcherConfigRejectsLeaseSlackLostAtStoragePrecision(t *testing.T) {
	t.Parallel()

	cfg := DispatcherConfig{
		BatchSize:      1,
		PollInterval:   time.Second,
		Lease:          3*time.Second + time.Nanosecond,
		MaxAttempts:    1,
		RetryBaseDelay: time.Second,
		RetryMaxDelay:  time.Second,
		PublishTimeout: time.Second,
		StoreTimeout:   time.Second,
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want persisted-lease precision error")
	}

	cfg.Lease = 3*time.Second + time.Microsecond
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() with one-microsecond lease slack error = %v", err)
	}
}

func TestDispatcherConfigRejectsOverflowingTimeoutBudget(t *testing.T) {
	t.Parallel()

	maxDuration := time.Duration(1<<63 - 1)
	cfg := DispatcherConfig{
		BatchSize:      1,
		PollInterval:   time.Second,
		Lease:          24 * time.Hour,
		MaxAttempts:    1,
		RetryBaseDelay: time.Second,
		RetryMaxDelay:  time.Second,
		PublishTimeout: maxDuration,
		StoreTimeout:   maxDuration,
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want overflowing budget error")
	}
}

func TestDispatcherBoundsClaimWithStoreTimeout(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("claim stopped")
	store := &fakeEventStore{
		claimFn: func(ctx context.Context, _ ClaimConfig) ([]ClaimedEvent, error) {
			deadline, ok := ctx.Deadline()
			if !ok {
				t.Fatal("Claim() context has no deadline")
			}
			if remaining := time.Until(deadline); remaining <= 0 || remaining > 2*time.Second {
				t.Fatalf("Claim() deadline remaining = %v, want bounded positive duration", remaining)
			}
			return nil, sentinel
		},
	}
	dispatcher := mustDispatcher(
		t,
		store,
		func(context.Context, string, string, []byte) error { return nil },
		nil,
	)

	err := dispatcher.Run(context.Background())
	if !errors.Is(err, sentinel) {
		t.Fatalf("Run() error = %v, want wrapped sentinel", err)
	}
	if got := err.Error(); got != "claim outbox dispatch batch" {
		t.Fatalf("Run() error text = %q", got)
	}
}

func TestDispatcherReturnsStorageFailure(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("database unavailable")
	store := &fakeEventStore{settleErr: sentinel}
	dispatcher := mustDispatcher(t, store, func(context.Context, string, string, []byte) error {
		return nil
	}, nil)

	err := dispatcher.dispatchOne(
		context.Background(),
		ClaimedEvent{ID: 1, LockToken: "token"},
	)
	if !errors.Is(err, sentinel) {
		t.Fatalf("dispatchOne() error = %v, want sentinel", err)
	}
	if got := err.Error(); got != "mark outbox event published" {
		t.Fatalf("dispatchOne() error text = %q", got)
	}
}

func TestRetryDelayIsExponentiallyCapped(t *testing.T) {
	t.Parallel()

	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{attempt: 1, want: time.Second},
		{attempt: 2, want: 2 * time.Second},
		{attempt: 3, want: 4 * time.Second},
		{attempt: 4, want: 5 * time.Second},
		{attempt: 100, want: 5 * time.Second},
	}
	for _, tt := range tests {
		if got := retryDelay(tt.attempt, time.Second, 5*time.Second); got != tt.want {
			t.Fatalf("retryDelay(%d) = %v, want %v", tt.attempt, got, tt.want)
		}
	}
}

func TestRetryDelayForEventIsDeterministicAndBounded(t *testing.T) {
	t.Parallel()

	const (
		eventID = "event-123"
		attempt = 4
	)
	first := retryDelayForEvent(eventID, attempt, time.Second, 5*time.Second)
	second := retryDelayForEvent(eventID, attempt, time.Second, 5*time.Second)
	if first != second {
		t.Fatalf("retry delay changed for same event/attempt: first=%v second=%v", first, second)
	}
	if first < 3750*time.Millisecond || first > 5*time.Second {
		t.Fatalf("retry delay = %v, want 3.75s..5s", first)
	}
	if first%time.Microsecond != 0 {
		t.Fatalf("retry delay = %v, want microsecond precision", first)
	}
}

func TestRetryDelayForEventRespectsMaximumAtLargestAllowedWindow(t *testing.T) {
	t.Parallel()

	got := retryDelayForEvent(
		"event-long-backoff",
		100,
		time.Second,
		24*time.Hour,
	)
	if got < 18*time.Hour || got > 24*time.Hour {
		t.Fatalf("retry delay = %v, want 18h..24h", got)
	}
	if got%time.Microsecond != 0 {
		t.Fatalf("retry delay = %v, want microsecond precision", got)
	}
}

func TestRetryDelayForEventKeepsFirstAttemptAtBase(t *testing.T) {
	t.Parallel()

	got := retryDelayForEvent("event-1", 1, time.Second, time.Minute)
	if got != time.Second {
		t.Fatalf("first retry delay = %v, want 1s", got)
	}
}

func TestRetryDelayForEventSpreadsDifferentEvents(t *testing.T) {
	t.Parallel()

	delays := make(map[time.Duration]struct{})
	for i := 0; i < 32; i++ {
		eventID := fmt.Sprintf("event-%d", i)
		delays[retryDelayForEvent(eventID, 6, time.Second, time.Minute)] = struct{}{}
	}
	if len(delays) < 2 {
		t.Fatalf("jitter produced only %d distinct delay(s), want multiple", len(delays))
	}
}

func mustDispatcher(
	t *testing.T,
	store EventStore,
	publisher Publisher,
	propagator coreprop.TextMapPropagator,
) *Dispatcher {
	t.Helper()

	dispatcher, err := NewDispatcher(
		store,
		publisher,
		propagator,
		DispatcherConfig{
			BatchSize:      10,
			PollInterval:   time.Millisecond,
			Lease:          10 * time.Second,
			MaxAttempts:    5,
			RetryBaseDelay: time.Second,
			RetryMaxDelay:  time.Minute,
			PublishTimeout: time.Second,
			StoreTimeout:   time.Second,
		},
	)
	if err != nil {
		t.Fatalf("NewDispatcher() error = %v", err)
	}
	return dispatcher
}

func TestDispatcherPreservesAllFatalBatchErrors(t *testing.T) {
	t.Parallel()

	first := errors.New("first settlement failure")
	second := errors.New("second settlement failure")
	release := make(chan struct{})

	store := &fakeEventStore{}
	store.markPublishedFn = func(_ context.Context, event ClaimedEvent) error {
		<-release
		switch event.ID {
		case 1:
			return first
		case 2:
			return second
		default:
			return nil
		}
	}

	started := make(chan struct{}, 2)
	dispatcher := mustDispatcher(
		t,
		store,
		func(context.Context, string, string, []byte) error {
			started <- struct{}{}
			return nil
		},
		nil,
	)

	done := make(chan error, 1)
	go func() {
		done <- dispatcher.dispatchBatch(context.Background(), []ClaimedEvent{
			{
				ID:        1,
				EventID:   "event-1",
				Subject:   "example.1",
				Attempts:  1,
				LockToken: "token-1",
			},
			{
				ID:        2,
				EventID:   "event-2",
				Subject:   "example.2",
				Attempts:  1,
				LockToken: "token-2",
			},
		})
	}()

	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("claimed batch did not start both publishers")
		}
	}
	close(release)

	select {
	case err := <-done:
		if !errors.Is(err, first) {
			t.Fatalf("dispatchBatch() error = %v, want first failure", err)
		}
		if !errors.Is(err, second) {
			t.Fatalf("dispatchBatch() error = %v, want second failure", err)
		}
	case <-time.After(time.Second):
		t.Fatal("dispatchBatch() did not finish")
	}
}
