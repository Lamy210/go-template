package outbox

import (
	"context"
	"errors"
	"testing"
	"time"

	coreprop "github.com/Lamy210/go-template/internal/core/propagation"
	"github.com/jackc/pgx/v5/pgconn"
)

type enqueueDBTX struct {
	args []any
	err  error
}

func (db *enqueueDBTX) Exec(
	_ context.Context,
	_ string,
	args ...any,
) (pgconn.CommandTag, error) {
	db.args = append([]any(nil), args...)
	return pgconn.CommandTag{}, db.err
}

type testPropagator struct{}

func (testPropagator) Inject(_ context.Context, carrier coreprop.TextMapCarrier) {
	carrier.Set("Traceparent", "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01")
	carrier.Set("Tracestate", "vendor=value")
	carrier.Set("Baggage", "secret=must-not-persist")
}

func (testPropagator) Extract(ctx context.Context, _ coreprop.TextMapCarrier) context.Context {
	return ctx
}

func TestEnqueuePersistsOnlyTraceContextMetadata(t *testing.T) {
	t.Parallel()

	db := &enqueueDBTX{}
	err := Enqueue(
		context.Background(),
		db,
		Event{ID: "event-1", Subject: "example.created", Payload: []byte("payload")},
		testPropagator{},
	)
	if err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	if len(db.args) != 5 {
		t.Fatalf("Exec args = %d, want 5", len(db.args))
	}
	if got := db.args[3]; got != "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01" {
		t.Fatalf("traceparent = %v", got)
	}
	if got := db.args[4]; got != "vendor=value" {
		t.Fatalf("tracestate = %v", got)
	}
	for _, arg := range db.args {
		if got, ok := arg.(string); ok && got == "secret=must-not-persist" {
			t.Fatal("baggage was persisted")
		}
	}
}

func TestEnqueueSanitizesDatabaseErrors(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("postgres diagnostic with secret")
	db := &enqueueDBTX{err: sentinel}
	err := Enqueue(
		context.Background(),
		db,
		Event{ID: "event-1", Subject: "example.created"},
		nil,
	)
	if !errors.Is(err, sentinel) {
		t.Fatalf("Enqueue() error = %v, want wrapped sentinel", err)
	}
	if got := err.Error(); got != "enqueue outbox event" {
		t.Fatalf("Enqueue() error text = %q", got)
	}
}

func TestEventValidateBounds(t *testing.T) {
	t.Parallel()

	tests := []Event{
		{},
		{ID: " event-1", Subject: "subject"},
		{ID: "event-1 ", Subject: "subject"},
		{ID: "event\n1", Subject: "subject"},
		{ID: "id", Subject: ""},
		{ID: "id", Subject: "subject", Payload: make([]byte, maxPayloadBytes+1)},
	}
	for _, event := range tests {
		if err := event.Validate(); err == nil {
			t.Fatalf("Validate(%+v) error = nil, want error", event)
		}
	}
}

func TestClaimConfigValidate(t *testing.T) {
	t.Parallel()

	for _, cfg := range []ClaimConfig{
		{},
		{BatchSize: 1001, Lease: time.Second},
		{BatchSize: 1, Lease: 0},
	} {
		if err := cfg.Validate(); err == nil {
			t.Fatalf("Validate(%+v) error = nil, want error", cfg)
		}
	}
}

func TestDurationIntervalUsesIntegerMicroseconds(t *testing.T) {
	t.Parallel()

	if got, want := durationInterval(1500*time.Microsecond), "1500 microseconds"; got != want {
		t.Fatalf("durationInterval() = %q, want %q", got, want)
	}
}
