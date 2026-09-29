package messaging

import (
	"context"
	"errors"
	"testing"

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
