package outbox

import (
	"context"
	"errors"
	"strings"
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

type staticTracePropagator struct {
	traceparent string
	tracestate  string
}

func (p staticTracePropagator) Inject(_ context.Context, carrier coreprop.TextMapCarrier) {
	if p.traceparent != "" {
		carrier.Set("traceparent", p.traceparent)
	}
	if p.tracestate != "" {
		carrier.Set("tracestate", p.tracestate)
	}
}

func (staticTracePropagator) Extract(
	ctx context.Context,
	_ coreprop.TextMapCarrier,
) context.Context {
	return ctx
}

type panicInjectPropagator struct{}

func (panicInjectPropagator) Inject(_ context.Context, carrier coreprop.TextMapCarrier) {
	carrier.Set("traceparent", "partial-trace-context")
	panic("sensitive propagation inject panic")
}

func (panicInjectPropagator) Extract(ctx context.Context, _ coreprop.TextMapCarrier) context.Context {
	return ctx
}

func TestEnqueueContainsPropagationPanicAndDropsPartialMetadata(t *testing.T) {
	t.Parallel()

	db := &enqueueDBTX{}
	err := Enqueue(
		context.Background(),
		db,
		Event{ID: "event-1", Subject: "example.created", Payload: []byte("payload")},
		panicInjectPropagator{},
	)
	if err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	if len(db.args) != 5 {
		t.Fatalf("Exec args = %d, want 5", len(db.args))
	}
	if got := db.args[3]; got != "" {
		t.Fatalf("traceparent = %v, want empty after propagation panic", got)
	}
	if got := db.args[4]; got != "" {
		t.Fatalf("tracestate = %v, want empty after propagation panic", got)
	}
}

func TestEnqueueDropsUnsafePropagationMetadata(t *testing.T) {
	t.Parallel()

	validTraceparent := "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01"
	tests := []struct {
		name            string
		propagator      staticTracePropagator
		wantTraceparent string
		wantTracestate  string
	}{
		{
			name: "missing traceparent drops tracestate",
			propagator: staticTracePropagator{
				tracestate: "vendor=value",
			},
		},
		{
			name: "oversized traceparent drops both",
			propagator: staticTracePropagator{
				traceparent: strings.Repeat("a", maxTraceparentLen+1),
				tracestate:  "vendor=value",
			},
		},
		{
			name: "invalid UTF-8 traceparent drops both",
			propagator: staticTracePropagator{
				traceparent: "00-" + string([]byte{0xff}),
				tracestate:  "vendor=value",
			},
		},
		{
			name: "NUL traceparent drops both",
			propagator: staticTracePropagator{
				traceparent: validTraceparent + "\x00",
				tracestate:  "vendor=value",
			},
		},
		{
			name: "oversized tracestate keeps traceparent",
			propagator: staticTracePropagator{
				traceparent: validTraceparent,
				tracestate:  strings.Repeat("a", maxTracestateLen+1),
			},
			wantTraceparent: validTraceparent,
		},
		{
			name: "invalid UTF-8 tracestate keeps traceparent",
			propagator: staticTracePropagator{
				traceparent: validTraceparent,
				tracestate:  "vendor=" + string([]byte{0xff}),
			},
			wantTraceparent: validTraceparent,
		},
		{
			name: "NUL tracestate keeps traceparent",
			propagator: staticTracePropagator{
				traceparent: validTraceparent,
				tracestate:  "vendor=value\x00",
			},
			wantTraceparent: validTraceparent,
		},
		{
			name: "safe metadata is preserved",
			propagator: staticTracePropagator{
				traceparent: validTraceparent,
				tracestate:  "vendor=value",
			},
			wantTraceparent: validTraceparent,
			wantTracestate:  "vendor=value",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := &enqueueDBTX{}
			err := Enqueue(
				context.Background(),
				db,
				Event{ID: "event-1", Subject: "example.created"},
				tt.propagator,
			)
			if err != nil {
				t.Fatalf("Enqueue() error = %v", err)
			}
			if len(db.args) != 5 {
				t.Fatalf("Exec args = %d, want 5", len(db.args))
			}
			if got := db.args[3]; got != tt.wantTraceparent {
				t.Fatalf("traceparent = %q, want %q", got, tt.wantTraceparent)
			}
			if got := db.args[4]; got != tt.wantTracestate {
				t.Fatalf("tracestate = %q, want %q", got, tt.wantTracestate)
			}
		})
	}
}

func TestEnqueueNormalizesNilPayloadToEmptyBytes(t *testing.T) {
	t.Parallel()

	db := &enqueueDBTX{}
	if err := Enqueue(
		context.Background(),
		db,
		Event{ID: "event-empty", Subject: "example.empty", Payload: nil},
		nil,
	); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	if len(db.args) != 5 {
		t.Fatalf("Exec args = %d, want 5", len(db.args))
	}

	payload, ok := db.args[2].([]byte)
	if !ok {
		t.Fatalf("payload arg type = %T, want []byte", db.args[2])
	}
	if payload == nil {
		t.Fatal("payload arg is nil, want explicit empty []byte")
	}
	if len(payload) != 0 {
		t.Fatalf("payload len = %d, want 0", len(payload))
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
		{ID: "event\x001", Subject: "subject"},
		{ID: string([]byte{0xff}), Subject: "subject"},
		{ID: "event-" + string([]byte{0xc3, 0x28}), Subject: "subject"},
		{ID: "id", Subject: ""},
		{ID: "id", Subject: "subject\x00value"},
		{ID: "id", Subject: string([]byte{0xff})},
		{ID: "id", Subject: "subject." + string([]byte{0xc3, 0x28})},
		{ID: "id", Subject: "subject", Payload: make([]byte, maxPayloadBytes+1)},
	}
	for _, event := range tests {
		if err := event.Validate(); err == nil {
			t.Fatalf("Validate(%+v) error = nil, want error", event)
		}
	}
}

func TestEnqueueRejectsInvalidUTF8SubjectBeforeDatabaseUse(t *testing.T) {
	t.Parallel()

	db := &enqueueDBTX{}
	err := Enqueue(
		context.Background(),
		db,
		Event{
			ID:      "event-1",
			Subject: "events." + string([]byte{0xff}),
		},
		nil,
	)
	if err == nil {
		t.Fatal("Enqueue() error = nil, want invalid subject error")
	}
	if len(db.args) != 0 {
		t.Fatalf("database Exec called with %d arguments", len(db.args))
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
