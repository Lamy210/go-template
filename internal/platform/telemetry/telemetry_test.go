package telemetry

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	otelprop "go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

func TestSignalEndpointAppendsSignalPath(t *testing.T) {
	t.Parallel()

	got, err := signalEndpoint("https://collector.example.com/otel/", "traces")
	if err != nil {
		t.Fatalf("signalEndpoint() error = %v", err)
	}
	want := "https://collector.example.com/otel/v1/traces"
	if got != want {
		t.Fatalf("signalEndpoint() = %q, want %q", got, want)
	}
}

func TestConfigRejectsRetryBeyondExportDeadline(t *testing.T) {
	t.Parallel()

	cfg := testConfig()
	cfg.RetryMaxElapsedTime = cfg.ExportTimeout + time.Second
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want retry deadline error")
	}
}

func TestOperationErrorRetainsCauseWithoutExposingIt(t *testing.T) {
	t.Parallel()

	cause := errors.New("collector response with sensitive diagnostic")
	err := newOperationError("export telemetry", cause)
	if !errors.Is(err, cause) {
		t.Fatal("operation error does not retain cause")
	}
	if strings.Contains(err.Error(), cause.Error()) {
		t.Fatalf("Error() exposed raw exporter error: %q", err.Error())
	}
}

func TestLogAttrsExtractsTraceCorrelation(t *testing.T) {
	t.Parallel()

	traceID := trace.TraceID{0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef, 0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef}
	spanID := trace.SpanID{0x10, 0x32, 0x54, 0x76, 0x98, 0xba, 0xdc, 0xfe}
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID,
		SpanID:  spanID,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), spanContext)

	attrs := LogAttrs(ctx)
	if len(attrs) != 2 {
		t.Fatalf("LogAttrs() len = %d, want 2", len(attrs))
	}
	if attrs[0].Key != "trace_id" || attrs[0].Value.String() != traceID.String() {
		t.Fatalf("trace attr = %#v", attrs[0])
	}
	if attrs[1].Key != "span_id" || attrs[1].Value.String() != spanID.String() {
		t.Fatalf("span attr = %#v", attrs[1])
	}
}

func TestLogAttrsWithoutSpanIsEmpty(t *testing.T) {
	t.Parallel()

	if attrs := LogAttrs(context.Background()); len(attrs) != 0 {
		t.Fatalf("LogAttrs() = %#v, want empty", attrs)
	}
}

func TestProviderInjectsAndExtractsW3CTraceContext(t *testing.T) {
	t.Parallel()

	provider := &Provider{
		propagator: otelprop.NewCompositeTextMapPropagator(
			otelprop.TraceContext{},
			otelprop.Baggage{},
		),
	}
	traceID := trace.TraceID{0x10, 0x32, 0x54, 0x76, 0x98, 0xba, 0xdc, 0xfe, 0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef}
	spanID := trace.SpanID{0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef}
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), spanContext)
	carrier := otelprop.MapCarrier{}

	provider.Inject(ctx, carrier)
	if carrier.Get("traceparent") == "" {
		t.Fatal("traceparent was not injected")
	}

	extracted := provider.Extract(context.Background(), carrier)
	got := trace.SpanContextFromContext(extracted)
	if got.TraceID() != traceID {
		t.Fatalf("extracted trace ID = %s, want %s", got.TraceID(), traceID)
	}
	if got.SpanID() != spanID {
		t.Fatalf("extracted span ID = %s, want %s", got.SpanID(), spanID)
	}
	if !got.IsRemote() {
		t.Fatal("extracted span context is not remote")
	}
}

func testConfig() Config {
	return Config{
		Endpoint:                "http://127.0.0.1:4318",
		ExportTimeout:           15 * time.Second,
		RetryInitialInterval:    500 * time.Millisecond,
		RetryMaxInterval:        2 * time.Second,
		RetryMaxElapsedTime:     10 * time.Second,
		MetricInterval:          30 * time.Second,
		TraceSampleRatio:        1,
		TraceMaxQueueSize:       2048,
		TraceMaxExportBatchSize: 512,
		TraceBatchTimeout:       5 * time.Second,
		MaxExportRequestBytes:   4 << 20,
	}
}

func TestProviderExportsTracesAndMetricsOverHTTP(t *testing.T) {
	var mu sync.Mutex
	requests := map[string]int{}

	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests[r.URL.Path]++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(collector.Close)

	cfg := testConfig()
	cfg.Endpoint = collector.URL
	cfg.MetricInterval = time.Hour
	cfg.TraceBatchTimeout = time.Hour

	provider, err := Open(
		context.Background(),
		cfg,
		ResourceConfig{
			ServiceName: "telemetry-test",
			Version:     "test",
			Environment: "test",
		},
		nil,
	)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	ctx := context.Background()
	_, span := provider.tracerProvider.Tracer("telemetry-test").Start(ctx, "work")
	span.End()

	counter, err := provider.meterProvider.Meter("telemetry-test").Int64Counter("work.count")
	if err != nil {
		t.Fatalf("create counter: %v", err)
	}
	counter.Add(ctx, 1)

	flushCtx, cancelFlush := context.WithTimeout(context.Background(), 5*time.Second)
	if err := provider.ForceFlush(flushCtx); err != nil {
		cancelFlush()
		t.Fatalf("ForceFlush() error = %v", err)
	}
	cancelFlush()

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	if err := provider.Shutdown(shutdownCtx); err != nil {
		cancelShutdown()
		t.Fatalf("Shutdown() error = %v", err)
	}
	cancelShutdown()

	mu.Lock()
	defer mu.Unlock()
	if requests["/v1/traces"] == 0 {
		t.Fatalf("trace export requests = %v, want /v1/traces", requests)
	}
	if requests["/v1/metrics"] == 0 {
		t.Fatalf("metric export requests = %v, want /v1/metrics", requests)
	}
}
