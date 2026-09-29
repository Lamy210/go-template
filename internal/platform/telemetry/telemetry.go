// Package telemetry contains the optional OpenTelemetry traces/metrics profile.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Lamy210/go-template/internal/core/safeerror"
	coreprop "github.com/Lamy210/go-template/internal/core/propagation"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
)

const instrumentationName = "github.com/Lamy210/go-template/internal/platform/telemetry"

// Config contains bounded OTLP/HTTP exporter and SDK settings.
type Config struct {
	Endpoint                string
	ExportTimeout           time.Duration
	RetryInitialInterval    time.Duration
	RetryMaxInterval        time.Duration
	RetryMaxElapsedTime     time.Duration
	MetricInterval          time.Duration
	TraceSampleRatio        float64
	TraceMaxQueueSize       int
	TraceMaxExportBatchSize int
	TraceBatchTimeout       time.Duration
	MaxExportRequestBytes   int
}

// ResourceConfig identifies the service producing telemetry.
type ResourceConfig struct {
	ServiceName string
	Version     string
	Environment string
}

// Provider owns the trace/metric SDK lifecycle and HTTP instrumentation.
type Provider struct {
	tracerProvider *sdktrace.TracerProvider
	meterProvider  *metric.MeterProvider
	propagator     propagation.TextMapPropagator

	shutdownOnce sync.Once
	shutdownErr  error
}

var _ coreprop.TextMapPropagator = (*Provider)(nil)

// Open builds OTLP/HTTP trace and metric pipelines without requiring the
// collector to be reachable during service startup.
func Open(
	ctx context.Context,
	cfg Config,
	resourceCfg ResourceConfig,
	logger *slog.Logger,
) (*Provider, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(resourceCfg.ServiceName) == "" {
		return nil, fmt.Errorf("telemetry service name must not be empty")
	}
	if logger == nil {
		logger = slog.Default()
	}

	// OTel processors report asynchronous export failures through the global
	// handler. Do not log the raw error because exporter diagnostics may contain
	// endpoint or backend response details.
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(error) {
		logger.Warn("telemetry pipeline error")
	}))

	traceEndpoint, err := signalEndpoint(cfg.Endpoint, "traces")
	if err != nil {
		return nil, err
	}
	metricEndpoint, err := signalEndpoint(cfg.Endpoint, "metrics")
	if err != nil {
		return nil, err
	}

	traceExporter, err := otlptracehttp.New(
		ctx,
		otlptracehttp.WithEndpointURL(traceEndpoint),
		otlptracehttp.WithTimeout(cfg.ExportTimeout),
		otlptracehttp.WithMaxRequestSize(cfg.MaxExportRequestBytes),
		otlptracehttp.WithRetry(otlptracehttp.RetryConfig{
			Enabled:         true,
			InitialInterval: cfg.RetryInitialInterval,
			MaxInterval:     cfg.RetryMaxInterval,
			MaxElapsedTime:  cfg.RetryMaxElapsedTime,
		}),
	)
	if err != nil {
		return nil, safeerror.Wrap("create OTLP trace exporter", err)
	}

	metricExporter, err := otlpmetrichttp.New(
		ctx,
		otlpmetrichttp.WithEndpointURL(metricEndpoint),
		otlpmetrichttp.WithTimeout(cfg.ExportTimeout),
		otlpmetrichttp.WithMaxRequestSize(cfg.MaxExportRequestBytes),
		otlpmetrichttp.WithRetry(otlpmetrichttp.RetryConfig{
			Enabled:         true,
			InitialInterval: cfg.RetryInitialInterval,
			MaxInterval:     cfg.RetryMaxInterval,
			MaxElapsedTime:  cfg.RetryMaxElapsedTime,
		}),
	)
	if err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), cfg.ExportTimeout)
		defer cancel()
		_ = traceExporter.Shutdown(cleanupCtx)
		return nil, safeerror.Wrap("create OTLP metric exporter", err)
	}

	res := resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(resourceCfg.ServiceName),
		semconv.ServiceVersion(resourceCfg.Version),
		semconv.DeploymentEnvironmentNameKey.String(resourceCfg.Environment),
	)

	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithSampler(
			sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.TraceSampleRatio)),
		),
		sdktrace.WithBatcher(
			traceExporter,
			sdktrace.WithMaxQueueSize(cfg.TraceMaxQueueSize),
			sdktrace.WithMaxExportBatchSize(cfg.TraceMaxExportBatchSize),
			sdktrace.WithBatchTimeout(cfg.TraceBatchTimeout),
			sdktrace.WithExportTimeout(cfg.ExportTimeout),
		),
	)

	reader := metric.NewPeriodicReader(
		metricExporter,
		metric.WithInterval(cfg.MetricInterval),
		metric.WithTimeout(cfg.ExportTimeout),
	)
	meterProvider := metric.NewMeterProvider(
		metric.WithResource(res),
		metric.WithReader(reader),
	)

	propagator := propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	)

	otel.SetTracerProvider(tracerProvider)
	otel.SetMeterProvider(meterProvider)
	otel.SetTextMapPropagator(propagator)

	return &Provider{
		tracerProvider: tracerProvider,
		meterProvider:  meterProvider,
		propagator:     propagator,
	}, nil
}

// Validate rejects unbounded or malformed telemetry SDK settings.
func (c Config) Validate() error {
	if _, err := signalEndpoint(c.Endpoint, "traces"); err != nil {
		return err
	}
	if c.ExportTimeout <= 0 {
		return fmt.Errorf("telemetry export timeout must be positive")
	}
	if c.RetryInitialInterval <= 0 || c.RetryMaxInterval <= 0 || c.RetryMaxElapsedTime <= 0 {
		return fmt.Errorf("telemetry retry durations must be positive")
	}
	if c.RetryMaxInterval < c.RetryInitialInterval {
		return fmt.Errorf("telemetry retry max interval must not be less than initial interval")
	}
	if c.RetryMaxElapsedTime < c.RetryInitialInterval {
		return fmt.Errorf("telemetry retry max elapsed time must not be less than initial interval")
	}
	if c.RetryMaxElapsedTime > c.ExportTimeout {
		return fmt.Errorf("telemetry retry max elapsed time must not exceed export timeout")
	}
	if c.MetricInterval <= 0 || c.TraceBatchTimeout <= 0 {
		return fmt.Errorf("telemetry metric and trace batch intervals must be positive")
	}
	if c.TraceSampleRatio < 0 || c.TraceSampleRatio > 1 {
		return fmt.Errorf("telemetry trace sample ratio must be between 0 and 1")
	}
	if c.TraceMaxQueueSize <= 0 || c.TraceMaxExportBatchSize <= 0 {
		return fmt.Errorf("telemetry trace queue and batch sizes must be positive")
	}
	if c.TraceMaxExportBatchSize > c.TraceMaxQueueSize {
		return fmt.Errorf("telemetry trace batch size must not exceed queue size")
	}
	if c.MaxExportRequestBytes <= 0 {
		return fmt.Errorf("telemetry max export request bytes must be positive")
	}
	return nil
}

// HTTPMiddleware returns OpenTelemetry net/http server instrumentation using
// this provider's explicit SDKs and W3C propagation.
func (p *Provider) HTTPMiddleware(operation string) func(http.Handler) http.Handler {
	return otelhttp.NewMiddleware(
		operation,
		otelhttp.WithTracerProvider(p.tracerProvider),
		otelhttp.WithMeterProvider(p.meterProvider),
		otelhttp.WithPropagators(p.propagator),
	)
}

// ObserveHTTPRoute updates the active server span after chi has resolved a
// low-cardinality route pattern. Raw URL paths are never used as a fallback.
func (*Provider) ObserveHTTPRoute(ctx context.Context, method, route string) {
	route = strings.TrimSpace(route)
	if route == "" {
		return
	}

	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}

	span.SetName(httpSpanMethod(method) + " " + route)
	span.SetAttributes(attribute.String("http.route", route))
}

func httpSpanMethod(method string) string {
	switch method {
	case http.MethodConnect,
		http.MethodDelete,
		http.MethodGet,
		http.MethodHead,
		http.MethodOptions,
		http.MethodPatch,
		http.MethodPost,
		http.MethodPut,
		http.MethodTrace:
		return method
	default:
		return "HTTP"
	}
}

// Inject writes the active cross-process context into a transport-neutral carrier.
func (p *Provider) Inject(ctx context.Context, carrier coreprop.TextMapCarrier) {
	p.propagator.Inject(ctx, carrier)
}

// Extract restores cross-process context from a transport-neutral carrier.
func (p *Provider) Extract(ctx context.Context, carrier coreprop.TextMapCarrier) context.Context {
	return p.propagator.Extract(ctx, carrier)
}

// StartPublish starts one logical NATS publish span. The returned context is
// the message creation context and should be injected into the outgoing message.
func (p *Provider) StartPublish(
	ctx context.Context,
	destination string,
) (context.Context, func(error)) {
	return p.startMessagingOperation(
		ctx,
		"publish",
		"send",
		destination,
		trace.SpanKindProducer,
	)
}

// StartProcess starts one logical NATS handler-processing span.
func (p *Provider) StartProcess(
	ctx context.Context,
	destination string,
) (context.Context, func(error)) {
	return p.startMessagingOperation(
		ctx,
		"process",
		"process",
		destination,
		trace.SpanKindConsumer,
	)
}

func (p *Provider) startMessagingOperation(
	ctx context.Context,
	operationName string,
	operationType string,
	destination string,
	kind trace.SpanKind,
) (context.Context, func(error)) {
	tracer := p.tracerProvider.Tracer(instrumentationName)
	spanCtx, span := tracer.Start(
		ctx,
		operationName+" "+destination,
		trace.WithSpanKind(kind),
		trace.WithAttributes(
			attribute.String("messaging.system", "nats"),
			attribute.String("messaging.destination.name", destination),
			attribute.String("messaging.operation.name", operationName),
			attribute.String("messaging.operation.type", operationType),
		),
	)

	return spanCtx, func(err error) {
		if err != nil {
			span.SetAttributes(attribute.String("error.type", fmt.Sprintf("%T", err)))
			span.SetStatus(codes.Error, "messaging operation failed")
		}
		span.End()
	}
}

// LogAttrs exposes correlation identifiers from the active span without coupling callers
// to OpenTelemetry-specific types.
func LogAttrs(ctx context.Context) []slog.Attr {
	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		return nil
	}

	attrs := []slog.Attr{
		slog.String("trace_id", spanContext.TraceID().String()),
	}
	if spanContext.SpanID().IsValid() {
		attrs = append(attrs, slog.String("span_id", spanContext.SpanID().String()))
	}
	return attrs
}

// ForceFlush exports pending traces and metrics within the caller's deadline.
func (p *Provider) ForceFlush(ctx context.Context) error {
	if p == nil {
		return nil
	}
	return errors.Join(
		p.tracerProvider.ForceFlush(ctx),
		p.meterProvider.ForceFlush(ctx),
	)
}

// Shutdown flushes and releases the trace and metric pipelines once.
func (p *Provider) Shutdown(ctx context.Context) error {
	if p == nil {
		return nil
	}
	p.shutdownOnce.Do(func() {
		p.shutdownErr = errors.Join(
			p.meterProvider.Shutdown(ctx),
			p.tracerProvider.Shutdown(ctx),
		)
	})
	return p.shutdownErr
}

func signalEndpoint(base, signal string) (string, error) {
	endpoint, err := url.Parse(strings.TrimSpace(base))
	if err != nil {
		return "", safeerror.Wrap("parse OTLP endpoint", err)
	}
	if endpoint.Scheme != "http" && endpoint.Scheme != "https" {
		return "", fmt.Errorf("OTLP endpoint must use http or https")
	}
	if endpoint.Host == "" {
		return "", fmt.Errorf("OTLP endpoint must include a host")
	}
	if endpoint.User != nil {
		return "", fmt.Errorf("OTLP endpoint must not contain userinfo")
	}
	if endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return "", fmt.Errorf("OTLP endpoint must not contain query or fragment")
	}

	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/v1/" + signal
	endpoint.RawPath = ""
	return endpoint.String(), nil
}
