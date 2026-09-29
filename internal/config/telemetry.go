package config

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultTelemetryEnabled                 = false
	defaultTelemetryEndpoint                = "http://127.0.0.1:4318"
	defaultTelemetryExportTimeout           = 15 * time.Second
	defaultTelemetryRetryInitialInterval    = 500 * time.Millisecond
	defaultTelemetryRetryMaxInterval        = 2 * time.Second
	defaultTelemetryRetryMaxElapsedTime     = 10 * time.Second
	defaultTelemetryMetricInterval          = 30 * time.Second
	defaultTelemetryTraceSampleRatio        = 1.0
	defaultTelemetryTraceMaxQueueSize       = 2048
	defaultTelemetryTraceMaxExportBatchSize = 512
	defaultTelemetryTraceBatchTimeout       = 5 * time.Second
	defaultTelemetryMaxExportRequestBytes   = 4 << 20
	defaultTelemetryShutdownTimeout         = 10 * time.Second
)

// TelemetryConfig contains the optional OpenTelemetry traces/metrics profile.
type TelemetryConfig struct {
	Enabled                 bool
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
	ShutdownTimeout         time.Duration
}

type telemetryEndpointParseError struct {
	cause error
}

func (*telemetryEndpointParseError) Error() string {
	return "parse OTEL_EXPORTER_OTLP_ENDPOINT"
}

func (e *telemetryEndpointParseError) Unwrap() error {
	return e.cause
}

func loadTelemetry(lookup lookupEnv) (TelemetryConfig, error) {
	enabled, err := boolValue(lookup, "TELEMETRY_ENABLED", defaultTelemetryEnabled)
	if err != nil {
		return TelemetryConfig{}, err
	}

	cfg := defaultTelemetryConfig()
	cfg.Enabled = enabled
	if !enabled {
		// A disabled optional profile must not make the service fail because of
		// stale or otherwise irrelevant OpenTelemetry settings.
		return cfg, nil
	}

	exportTimeout, err := durationValue(
		lookup,
		"TELEMETRY_EXPORT_TIMEOUT",
		defaultTelemetryExportTimeout,
	)
	if err != nil {
		return TelemetryConfig{}, err
	}
	retryInitialInterval, err := durationValue(
		lookup,
		"TELEMETRY_RETRY_INITIAL_INTERVAL",
		defaultTelemetryRetryInitialInterval,
	)
	if err != nil {
		return TelemetryConfig{}, err
	}
	retryMaxInterval, err := durationValue(
		lookup,
		"TELEMETRY_RETRY_MAX_INTERVAL",
		defaultTelemetryRetryMaxInterval,
	)
	if err != nil {
		return TelemetryConfig{}, err
	}
	retryMaxElapsedTime, err := durationValue(
		lookup,
		"TELEMETRY_RETRY_MAX_ELAPSED_TIME",
		defaultTelemetryRetryMaxElapsedTime,
	)
	if err != nil {
		return TelemetryConfig{}, err
	}
	metricInterval, err := durationValue(
		lookup,
		"TELEMETRY_METRIC_INTERVAL",
		defaultTelemetryMetricInterval,
	)
	if err != nil {
		return TelemetryConfig{}, err
	}
	traceSampleRatio, err := float64Value(
		lookup,
		"TELEMETRY_TRACE_SAMPLE_RATIO",
		defaultTelemetryTraceSampleRatio,
	)
	if err != nil {
		return TelemetryConfig{}, err
	}
	traceMaxQueueSize, err := intValue(
		lookup,
		"TELEMETRY_TRACE_MAX_QUEUE_SIZE",
		defaultTelemetryTraceMaxQueueSize,
	)
	if err != nil {
		return TelemetryConfig{}, err
	}
	traceMaxExportBatchSize, err := intValue(
		lookup,
		"TELEMETRY_TRACE_MAX_EXPORT_BATCH_SIZE",
		defaultTelemetryTraceMaxExportBatchSize,
	)
	if err != nil {
		return TelemetryConfig{}, err
	}
	traceBatchTimeout, err := durationValue(
		lookup,
		"TELEMETRY_TRACE_BATCH_TIMEOUT",
		defaultTelemetryTraceBatchTimeout,
	)
	if err != nil {
		return TelemetryConfig{}, err
	}
	maxExportRequestBytes, err := intValue(
		lookup,
		"TELEMETRY_MAX_EXPORT_REQUEST_BYTES",
		defaultTelemetryMaxExportRequestBytes,
	)
	if err != nil {
		return TelemetryConfig{}, err
	}
	shutdownTimeout, err := durationValue(
		lookup,
		"TELEMETRY_SHUTDOWN_TIMEOUT",
		defaultTelemetryShutdownTimeout,
	)
	if err != nil {
		return TelemetryConfig{}, err
	}

	cfg.Endpoint = stringValue(lookup, "OTEL_EXPORTER_OTLP_ENDPOINT", defaultTelemetryEndpoint)
	cfg.ExportTimeout = exportTimeout
	cfg.RetryInitialInterval = retryInitialInterval
	cfg.RetryMaxInterval = retryMaxInterval
	cfg.RetryMaxElapsedTime = retryMaxElapsedTime
	cfg.MetricInterval = metricInterval
	cfg.TraceSampleRatio = traceSampleRatio
	cfg.TraceMaxQueueSize = traceMaxQueueSize
	cfg.TraceMaxExportBatchSize = traceMaxExportBatchSize
	cfg.TraceBatchTimeout = traceBatchTimeout
	cfg.MaxExportRequestBytes = maxExportRequestBytes
	cfg.ShutdownTimeout = shutdownTimeout

	if err := cfg.Validate(); err != nil {
		return TelemetryConfig{}, err
	}
	return cfg, nil
}

func defaultTelemetryConfig() TelemetryConfig {
	return TelemetryConfig{
		Enabled:                 defaultTelemetryEnabled,
		Endpoint:                defaultTelemetryEndpoint,
		ExportTimeout:           defaultTelemetryExportTimeout,
		RetryInitialInterval:    defaultTelemetryRetryInitialInterval,
		RetryMaxInterval:        defaultTelemetryRetryMaxInterval,
		RetryMaxElapsedTime:     defaultTelemetryRetryMaxElapsedTime,
		MetricInterval:          defaultTelemetryMetricInterval,
		TraceSampleRatio:        defaultTelemetryTraceSampleRatio,
		TraceMaxQueueSize:       defaultTelemetryTraceMaxQueueSize,
		TraceMaxExportBatchSize: defaultTelemetryTraceMaxExportBatchSize,
		TraceBatchTimeout:       defaultTelemetryTraceBatchTimeout,
		MaxExportRequestBytes:   defaultTelemetryMaxExportRequestBytes,
		ShutdownTimeout:         defaultTelemetryShutdownTimeout,
	}
}

// Validate rejects unbounded or malformed enabled telemetry profiles.
func (c TelemetryConfig) Validate() error {
	if !c.Enabled {
		return nil
	}

	endpoint, err := url.Parse(strings.TrimSpace(c.Endpoint))
	if err != nil {
		// net/url parse errors can include the original URL. Keep the cause
		// available for errors.Is/errors.As without copying a potentially
		// credential-bearing endpoint into normal error/log text.
		return &telemetryEndpointParseError{cause: err}
	}
	if endpoint.Scheme != "http" && endpoint.Scheme != "https" {
		return fmt.Errorf("OTEL_EXPORTER_OTLP_ENDPOINT must use http or https")
	}
	if endpoint.Host == "" {
		return fmt.Errorf("OTEL_EXPORTER_OTLP_ENDPOINT must include a host")
	}
	if endpoint.User != nil {
		return fmt.Errorf("OTEL_EXPORTER_OTLP_ENDPOINT must not contain userinfo")
	}
	if endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return fmt.Errorf("OTEL_EXPORTER_OTLP_ENDPOINT must not contain query or fragment")
	}
	if c.ExportTimeout <= 0 {
		return fmt.Errorf("TELEMETRY_EXPORT_TIMEOUT must be positive")
	}
	if c.RetryInitialInterval <= 0 || c.RetryMaxInterval <= 0 || c.RetryMaxElapsedTime <= 0 {
		return fmt.Errorf("telemetry retry durations must be positive")
	}
	if c.RetryMaxInterval < c.RetryInitialInterval {
		return fmt.Errorf("TELEMETRY_RETRY_MAX_INTERVAL must not be less than TELEMETRY_RETRY_INITIAL_INTERVAL")
	}
	if c.RetryMaxElapsedTime < c.RetryInitialInterval {
		return fmt.Errorf("TELEMETRY_RETRY_MAX_ELAPSED_TIME must not be less than TELEMETRY_RETRY_INITIAL_INTERVAL")
	}
	if c.RetryMaxElapsedTime > c.ExportTimeout {
		return fmt.Errorf("TELEMETRY_RETRY_MAX_ELAPSED_TIME must not exceed TELEMETRY_EXPORT_TIMEOUT")
	}
	if c.MetricInterval <= 0 {
		return fmt.Errorf("TELEMETRY_METRIC_INTERVAL must be positive")
	}
	if c.TraceSampleRatio < 0 || c.TraceSampleRatio > 1 {
		return fmt.Errorf("TELEMETRY_TRACE_SAMPLE_RATIO must be between 0 and 1")
	}
	if c.TraceMaxQueueSize <= 0 || c.TraceMaxExportBatchSize <= 0 {
		return fmt.Errorf("telemetry trace queue and batch sizes must be positive")
	}
	if c.TraceMaxExportBatchSize > c.TraceMaxQueueSize {
		return fmt.Errorf("TELEMETRY_TRACE_MAX_EXPORT_BATCH_SIZE must not exceed TELEMETRY_TRACE_MAX_QUEUE_SIZE")
	}
	if c.TraceBatchTimeout <= 0 {
		return fmt.Errorf("TELEMETRY_TRACE_BATCH_TIMEOUT must be positive")
	}
	if c.MaxExportRequestBytes <= 0 {
		return fmt.Errorf("TELEMETRY_MAX_EXPORT_REQUEST_BYTES must be positive")
	}
	if c.ShutdownTimeout <= 0 {
		return fmt.Errorf("TELEMETRY_SHUTDOWN_TIMEOUT must be positive")
	}
	return nil
}

func float64Value(lookup lookupEnv, key string, fallback float64) (float64, error) {
	value, ok := lookup(key)
	if !ok {
		return fallback, nil
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return parsed, nil
}
