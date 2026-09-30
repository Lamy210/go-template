package telemetry

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/codes"
	otelprop "go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
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

func TestConfigRejectsNonFiniteSampleRatio(t *testing.T) {
	t.Parallel()

	cfg := testConfig()
	cfg.TraceSampleRatio = math.NaN()
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want non-finite sample ratio error")
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

func TestZeroValueProviderMethodsAreSafe(t *testing.T) {
	t.Parallel()

	type contextKey struct{}
	ctx := context.WithValue(context.Background(), contextKey{}, "value")

	for _, provider := range []*Provider{nil, {}} {
		carrier := otelprop.MapCarrier{}
		provider.Inject(ctx, carrier)
		if len(carrier) != 0 {
			t.Fatalf("Inject() mutated carrier for zero-value provider: %#v", carrier)
		}
		if got := provider.Extract(ctx, carrier); got != ctx {
			t.Fatal("Extract() did not preserve context for zero-value provider")
		}

		for _, start := range []func(context.Context, string) (context.Context, func(error)){
			provider.StartPublish,
			provider.StartProcess,
		} {
			operationCtx, end := start(ctx, "orders.created")
			if operationCtx != ctx {
				t.Fatal("messaging operation changed context for zero-value provider")
			}
			end(errors.New("ignored"))
		}

		handler := provider.HTTPMiddleware("test")(
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}),
		)
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusNoContent {
			t.Fatalf("HTTP middleware status = %d, want %d", res.Code, http.StatusNoContent)
		}

		provider.ObserveHTTPRoute(ctx, http.MethodGet, "/widgets/{widgetID}")
		if err := provider.ForceFlush(ctx); err != nil {
			t.Fatalf("ForceFlush() error = %v", err)
		}
		if err := provider.Shutdown(ctx); err != nil {
			t.Fatalf("Shutdown() error = %v", err)
		}
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

func TestMessagingOperationSpans(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		start         func(*Provider) (context.Context, func(error))
		wantSpanName  string
		wantKind      trace.SpanKind
		wantOperation string
		wantType      string
		finishErr     error
		wantStatus    codes.Code
		wantErrorType string
	}{
		{
			name: "publish",
			start: func(provider *Provider) (context.Context, func(error)) {
				return provider.StartPublish(context.Background(), "orders.created")
			},
			wantSpanName:  "publish orders.created",
			wantKind:      trace.SpanKindProducer,
			wantOperation: "publish",
			wantType:      "send",
			wantStatus:    codes.Unset,
		},
		{
			name: "process error",
			start: func(provider *Provider) (context.Context, func(error)) {
				return provider.StartProcess(context.Background(), "orders.created")
			},
			wantSpanName:  "process orders.created",
			wantKind:      trace.SpanKindConsumer,
			wantOperation: "process",
			wantType:      "process",
			finishErr:     errors.New("sensitive handler diagnostic"),
			wantStatus:    codes.Error,
			wantErrorType: "*errors.errorString",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			recorder := tracetest.NewSpanRecorder()
			tracerProvider := sdktrace.NewTracerProvider(
				sdktrace.WithSpanProcessor(recorder),
			)
			t.Cleanup(func() {
				_ = tracerProvider.Shutdown(context.Background())
			})
			provider := &Provider{tracerProvider: tracerProvider}

			spanCtx, end := tt.start(provider)
			if !trace.SpanContextFromContext(spanCtx).IsValid() {
				t.Fatal("messaging span context is invalid")
			}
			end(tt.finishErr)

			ended := recorder.Ended()
			if len(ended) != 1 {
				t.Fatalf("ended spans = %d, want 1", len(ended))
			}
			span := ended[0]
			if span.Name() != tt.wantSpanName {
				t.Fatalf("span name = %q, want %q", span.Name(), tt.wantSpanName)
			}
			if span.SpanKind() != tt.wantKind {
				t.Fatalf("span kind = %v, want %v", span.SpanKind(), tt.wantKind)
			}

			attrs := map[string]string{}
			for _, attr := range span.Attributes() {
				attrs[string(attr.Key)] = attr.Value.AsString()
			}
			for key, want := range map[string]string{
				"messaging.system":           "nats",
				"messaging.destination.name": "orders.created",
				"messaging.operation.name":   tt.wantOperation,
				"messaging.operation.type":   tt.wantType,
			} {
				if got := attrs[key]; got != want {
					t.Fatalf("%s = %q, want %q", key, got, want)
				}
			}
			if span.Status().Code != tt.wantStatus {
				t.Fatalf("status = %v, want %v", span.Status().Code, tt.wantStatus)
			}
			if tt.wantErrorType == "" {
				if _, exists := attrs["error.type"]; exists {
					t.Fatalf("unexpected error.type = %q", attrs["error.type"])
				}
			} else if got := attrs["error.type"]; got != tt.wantErrorType {
				t.Fatalf("error.type = %q, want %q", got, tt.wantErrorType)
			}
			if len(span.Events()) != 0 {
				t.Fatalf("span events = %#v, want none so raw error text is not recorded", span.Events())
			}
		})
	}
}

func TestHTTPMiddlewareRouteObserverUpdatesActiveSpan(t *testing.T) {
	t.Parallel()

	recorder := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(recorder),
	)
	meterProvider := sdkmetric.NewMeterProvider()
	t.Cleanup(func() {
		_ = meterProvider.Shutdown(context.Background())
		_ = tracerProvider.Shutdown(context.Background())
	})

	provider := &Provider{
		tracerProvider: tracerProvider,
		meterProvider:  meterProvider,
		propagator:     otelprop.TraceContext{},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/widgets/{widgetID}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	routeAware := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(w, r)
		provider.ObserveHTTPRoute(r.Context(), r.Method, r.Pattern)
	})
	handler := provider.HTTPMiddleware("test-service")(routeAware)

	req := httptest.NewRequest(http.MethodGet, "/widgets/12345", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusNoContent)
	}

	ended := recorder.Ended()
	if len(ended) != 1 {
		t.Fatalf("ended spans = %d, want 1", len(ended))
	}
	span := ended[0]
	if span.Name() != "GET /widgets/{widgetID}" {
		t.Fatalf("span name = %q, want %q", span.Name(), "GET /widgets/{widgetID}")
	}
	if got, ok := spanStringAttribute(span, "http.route"); !ok || got != "/widgets/{widgetID}" {
		t.Fatalf("http.route = %q, present=%t", got, ok)
	}
}

func TestObserveHTTPRouteUpdatesServerSpan(t *testing.T) {
	t.Parallel()

	span := recordHTTPRouteSpan(t, http.MethodGet, "/widgets/{widgetID}")
	if span.Name() != "GET /widgets/{widgetID}" {
		t.Fatalf("span name = %q, want %q", span.Name(), "GET /widgets/{widgetID}")
	}
	if got, ok := spanStringAttribute(span, "http.route"); !ok || got != "/widgets/{widgetID}" {
		t.Fatalf("http.route = %q, present=%t", got, ok)
	}
}

func TestObserveHTTPRouteUsesHTTPForUnknownMethod(t *testing.T) {
	t.Parallel()

	span := recordHTTPRouteSpan(t, "BREW", "/widgets/{widgetID}")
	if span.Name() != "HTTP /widgets/{widgetID}" {
		t.Fatalf("span name = %q, want %q", span.Name(), "HTTP /widgets/{widgetID}")
	}
}

func TestObserveHTTPRouteIgnoresEmptyRoute(t *testing.T) {
	t.Parallel()

	span := recordHTTPRouteSpan(t, http.MethodGet, "")
	if span.Name() != "initial" {
		t.Fatalf("span name = %q, want initial", span.Name())
	}
	if got, ok := spanStringAttribute(span, "http.route"); ok {
		t.Fatalf("unexpected http.route = %q", got)
	}
}

func recordHTTPRouteSpan(
	t *testing.T,
	method string,
	route string,
) sdktrace.ReadOnlySpan {
	t.Helper()

	recorder := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(recorder),
	)
	t.Cleanup(func() {
		_ = tracerProvider.Shutdown(context.Background())
	})

	provider := &Provider{tracerProvider: tracerProvider}
	ctx, span := tracerProvider.Tracer("test").Start(
		context.Background(),
		"initial",
		trace.WithSpanKind(trace.SpanKindServer),
	)
	provider.ObserveHTTPRoute(ctx, method, route)
	span.End()

	ended := recorder.Ended()
	if len(ended) != 1 {
		t.Fatalf("ended spans = %d, want 1", len(ended))
	}
	return ended[0]
}

func spanStringAttribute(span sdktrace.ReadOnlySpan, key string) (string, bool) {
	for _, attr := range span.Attributes() {
		if string(attr.Key) == key {
			return attr.Value.AsString(), true
		}
	}
	return "", false
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
