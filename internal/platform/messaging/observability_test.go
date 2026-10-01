package messaging

import (
	"context"
	"errors"
	"testing"
	"time"

	coreprop "github.com/Lamy210/go-template/internal/core/propagation"
	"github.com/nats-io/nats.go"
)

type observabilityContextKey struct{}

type panickingPropagator struct{}

func (panickingPropagator) Inject(
	context.Context,
	coreprop.TextMapCarrier,
) {
	panic("sensitive inject panic")
}

func (panickingPropagator) Extract(
	context.Context,
	coreprop.TextMapCarrier,
) context.Context {
	panic("sensitive extract panic")
}

type extractedContextPropagator struct {
	ctx context.Context
}

func (p extractedContextPropagator) Inject(
	context.Context,
	coreprop.TextMapCarrier,
) {
}

func (p extractedContextPropagator) Extract(
	context.Context,
	coreprop.TextMapCarrier,
) context.Context {
	return p.ctx
}

type partialPanickingPropagator struct{}

func (partialPanickingPropagator) Inject(
	_ context.Context,
	carrier coreprop.TextMapCarrier,
) {
	carrier.Set("traceparent", "partial-value")
	panic("sensitive partial inject panic")
}

func (partialPanickingPropagator) Extract(
	ctx context.Context,
	_ coreprop.TextMapCarrier,
) context.Context {
	return ctx
}

type panickingStartTracer struct{}

func (panickingStartTracer) StartPublish(
	context.Context,
	string,
) (context.Context, func(error)) {
	panic("sensitive publish tracer panic")
}

func (panickingStartTracer) StartProcess(
	context.Context,
	string,
) (context.Context, func(error)) {
	panic("sensitive process tracer panic")
}

type panickingValueContext struct {
	context.Context
}

func (panickingValueContext) Value(any) any {
	panic("sensitive tracer context value panic")
}

type panickingFinishTracer struct{}

func (panickingFinishTracer) StartPublish(
	ctx context.Context,
	_ string,
) (context.Context, func(error)) {
	return context.WithValue(ctx, observabilityContextKey{}, "publish"), func(error) {
		panic("sensitive publish finish panic")
	}
}

func (panickingFinishTracer) StartProcess(
	ctx context.Context,
	_ string,
) (context.Context, func(error)) {
	return context.WithValue(ctx, observabilityContextKey{}, "process"), func(error) {
		panic("sensitive process finish panic")
	}
}

func TestInjectPropagationSafelyDiscardsPartialWritesAfterPanic(t *testing.T) {
	t.Parallel()

	header := nats.Header{}
	header.Set("X-Existing", "keep")
	client := &Client{propagator: partialPanickingPropagator{}}

	client.injectPropagationSafely(context.Background(), header)

	if got := header.Get("X-Existing"); got != "keep" {
		t.Fatalf("existing header = %q, want keep", got)
	}
	if got := header.Get("traceparent"); got != "" {
		t.Fatalf("partial traceparent escaped failed hook: %q", got)
	}
}

func TestCloneNATSHeaderDoesNotAliasValues(t *testing.T) {
	t.Parallel()

	original := nats.Header{
		"X-Business": {"keep"},
	}
	cloned := cloneNATSHeader(original)

	original["X-Business"][0] = "changed"
	original.Set("X-New", "new")

	if got := cloned.Get("X-Business"); got != "keep" {
		t.Fatalf("cloned business header = %q, want keep", got)
	}
	if got := cloned.Get("X-New"); got != "" {
		t.Fatalf("cloned new header = %q, want empty", got)
	}
}

func TestExtractPropagationSafelyFallsBackToInputContext(t *testing.T) {
	t.Parallel()

	ctx := context.WithValue(
		context.Background(),
		observabilityContextKey{},
		"original",
	)
	client := &Client{propagator: panickingPropagator{}}

	got := client.extractPropagationSafely(ctx, nats.Header{})

	if value, _ := got.Value(observabilityContextKey{}).(string); value != "original" {
		t.Fatalf("extracted context value = %q, want original", value)
	}
}

func TestExtractPropagationSafelyPreservesBusinessContextLifetime(t *testing.T) {
	t.Parallel()

	businessCause := errors.New("business canceled")
	remoteCause := errors.New("remote canceled")

	businessBase, cancelBusiness := context.WithCancelCause(context.Background())
	businessCtx, cancelDeadline := context.WithTimeout(businessBase, time.Minute)
	defer cancelDeadline()

	remoteCtx, cancelRemote := context.WithCancelCause(
		context.WithValue(
			context.Background(),
			observabilityContextKey{},
			"remote",
		),
	)
	cancelRemote(remoteCause)

	client := &Client{
		propagator: extractedContextPropagator{ctx: remoteCtx},
	}
	got := client.extractPropagationSafely(businessCtx, nats.Header{})

	if value, _ := got.Value(observabilityContextKey{}).(string); value != "remote" {
		t.Fatalf("extracted observability value = %q, want remote", value)
	}
	if err := got.Err(); err != nil {
		t.Fatalf("extracted context inherited remote cancellation: %v", err)
	}
	gotDeadline, gotOK := got.Deadline()
	wantDeadline, wantOK := businessCtx.Deadline()
	if gotOK != wantOK || !gotDeadline.Equal(wantDeadline) {
		t.Fatalf(
			"extracted deadline = (%v, %t), want (%v, %t)",
			gotDeadline,
			gotOK,
			wantDeadline,
			wantOK,
		)
	}

	cancelBusiness(businessCause)

	if !errors.Is(got.Err(), context.Canceled) {
		t.Fatalf("extracted context error = %v, want context.Canceled", got.Err())
	}
	cause := context.Cause(got)
	if !errors.Is(cause, businessCause) {
		t.Fatalf("extracted cancellation cause = %v, want business cause", cause)
	}
	if errors.Is(cause, remoteCause) {
		t.Fatalf("extracted cancellation cause leaked remote cause: %v", cause)
	}
}

func TestStartOperationSafelyContainsStartPanic(t *testing.T) {
	t.Parallel()

	ctx := context.WithValue(
		context.Background(),
		observabilityContextKey{},
		"original",
	)
	client := &Client{tracer: panickingStartTracer{}}

	tests := []struct {
		name  string
		start func(context.Context, string) (context.Context, func(error))
	}{
		{name: "publish", start: client.startPublishOperationSafely},
		{name: "process", start: client.startProcessOperationSafely},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotCtx, end := tt.start(ctx, "events.created")
			if value, _ := gotCtx.Value(observabilityContextKey{}).(string); value != "original" {
				t.Fatalf("operation context value = %q, want original", value)
			}
			end(errors.New("ignored by no-op finish"))
		})
	}
}

func TestStartOperationSafelyContainsTracerContextValuePanic(t *testing.T) {
	t.Parallel()

	ctx := context.WithValue(
		context.Background(),
		observabilityContextKey{},
		"business",
	)
	gotCtx, end := startOperationSafely(
		ctx,
		"events.created",
		func(context.Context, string) (context.Context, func(error)) {
			return panickingValueContext{Context: context.Background()}, nil
		},
	)
	defer end(nil)

	if value, _ := gotCtx.Value(observabilityContextKey{}).(string); value != "business" {
		t.Fatalf("operation context fallback value = %q, want business", value)
	}
}

func TestStartOperationSafelyPreservesCallerCancellationAndValues(t *testing.T) {
	t.Parallel()

	type businessContextKey struct{}
	base := context.WithValue(
		context.Background(),
		businessContextKey{},
		"business",
	)
	ctx, cancel := context.WithCancel(base)

	gotCtx, end := startOperationSafely(
		ctx,
		"events.created",
		func(context.Context, string) (context.Context, func(error)) {
			return context.WithValue(
				context.Background(),
				observabilityContextKey{},
				"traced",
			), nil
		},
	)
	defer end(nil)

	if value, _ := gotCtx.Value(observabilityContextKey{}).(string); value != "traced" {
		t.Fatalf("operation observability value = %q, want traced", value)
	}
	if value, _ := gotCtx.Value(businessContextKey{}).(string); value != "business" {
		t.Fatalf("operation business value = %q, want business", value)
	}

	cancel()
	select {
	case <-gotCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("operation context did not preserve caller cancellation")
	}
	if !errors.Is(gotCtx.Err(), context.Canceled) {
		t.Fatalf("operation context error = %v, want context.Canceled", gotCtx.Err())
	}
}

func TestStartOperationSafelyPreservesBusinessCancellationCause(t *testing.T) {
	t.Parallel()

	businessCause := errors.New("business canceled")
	tracerCause := errors.New("tracer canceled")

	businessCtx, cancelBusiness := context.WithCancelCause(context.Background())
	tracerCtx, cancelTracer := context.WithCancelCause(
		context.WithValue(
			context.Background(),
			observabilityContextKey{},
			"traced",
		),
	)
	cancelTracer(tracerCause)

	gotCtx, end := startOperationSafely(
		businessCtx,
		"events.created",
		func(context.Context, string) (context.Context, func(error)) {
			return tracerCtx, nil
		},
	)
	defer end(nil)

	if value, _ := gotCtx.Value(observabilityContextKey{}).(string); value != "traced" {
		t.Fatalf("operation observability value = %q, want traced", value)
	}
	cancelBusiness(businessCause)

	if !errors.Is(gotCtx.Err(), context.Canceled) {
		t.Fatalf("operation context error = %v, want context.Canceled", gotCtx.Err())
	}
	cause := context.Cause(gotCtx)
	if !errors.Is(cause, businessCause) {
		t.Fatalf("operation cancellation cause = %v, want business cause", cause)
	}
	if errors.Is(cause, tracerCause) {
		t.Fatalf("operation cancellation cause leaked tracer cause: %v", cause)
	}
}

func TestStartOperationSafelyIgnoresTracerCancellation(t *testing.T) {
	t.Parallel()

	tracerCtx, cancelTracer := context.WithCancel(
		context.WithValue(
			context.Background(),
			observabilityContextKey{},
			"traced",
		),
	)
	cancelTracer()

	gotCtx, end := startOperationSafely(
		context.Background(),
		"events.created",
		func(context.Context, string) (context.Context, func(error)) {
			return tracerCtx, nil
		},
	)
	defer end(nil)

	if value, _ := gotCtx.Value(observabilityContextKey{}).(string); value != "traced" {
		t.Fatalf("operation observability value = %q, want traced", value)
	}
	if err := gotCtx.Err(); err != nil {
		t.Fatalf("operation context inherited tracer cancellation: %v", err)
	}
	select {
	case <-gotCtx.Done():
		t.Fatal("operation context Done closed from tracer cancellation")
	default:
	}
}

func TestStartOperationSafelyContainsFinishPanic(t *testing.T) {
	t.Parallel()

	client := &Client{tracer: panickingFinishTracer{}}
	tests := []struct {
		name      string
		start     func(context.Context, string) (context.Context, func(error))
		wantValue string
	}{
		{
			name:      "publish",
			start:     client.startPublishOperationSafely,
			wantValue: "publish",
		},
		{
			name:      "process",
			start:     client.startProcessOperationSafely,
			wantValue: "process",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotCtx, end := tt.start(context.Background(), "events.created")
			if value, _ := gotCtx.Value(observabilityContextKey{}).(string); value != tt.wantValue {
				t.Fatalf("operation context value = %q, want %q", value, tt.wantValue)
			}
			end(errors.New("operation failed"))
		})
	}
}

func TestStartOperationSafelyNormalizesNilResults(t *testing.T) {
	t.Parallel()

	ctx := context.WithValue(
		context.Background(),
		observabilityContextKey{},
		"original",
	)
	gotCtx, end := startOperationSafely(
		ctx,
		"events.created",
		func(context.Context, string) (context.Context, func(error)) {
			return nil, nil
		},
	)

	if value, _ := gotCtx.Value(observabilityContextKey{}).(string); value != "original" {
		t.Fatalf("operation context value = %q, want original", value)
	}
	end(nil)
}
