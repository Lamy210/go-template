package outbox

import (
	"context"
	"errors"
	"testing"
	"time"

	coreprop "github.com/Lamy210/go-template/internal/core/propagation"
)

type outboxPropagationContextKey struct{}

type extractedOutboxContextPropagator struct {
	ctx context.Context
}

func (p extractedOutboxContextPropagator) Inject(
	context.Context,
	coreprop.TextMapCarrier,
) {
}

func (p extractedOutboxContextPropagator) Extract(
	context.Context,
	coreprop.TextMapCarrier,
) context.Context {
	return p.ctx
}

type panickingOutboxValueContext struct {
	context.Context
}

func (panickingOutboxValueContext) Value(any) any {
	panic("sensitive extracted context value panic")
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
			outboxPropagationContextKey{},
			"remote",
		),
	)
	cancelRemote(remoteCause)

	got := extractPropagationSafely(
		businessCtx,
		extractedOutboxContextPropagator{ctx: remoteCtx},
		newPropagationCarrier(),
	)

	if value, _ := got.Value(outboxPropagationContextKey{}).(string); value != "remote" {
		t.Fatalf("extracted propagation value = %q, want remote", value)
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

func TestExtractPropagationSafelyContainsExtractedValuePanic(t *testing.T) {
	t.Parallel()

	businessCtx := context.WithValue(
		context.Background(),
		outboxPropagationContextKey{},
		"business",
	)
	got := extractPropagationSafely(
		businessCtx,
		extractedOutboxContextPropagator{
			ctx: panickingOutboxValueContext{Context: context.Background()},
		},
		newPropagationCarrier(),
	)

	if value, _ := got.Value(outboxPropagationContextKey{}).(string); value != "business" {
		t.Fatalf("extracted fallback value = %q, want business", value)
	}
}
