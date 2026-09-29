package config

import (
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestTelemetryDisabledIgnoresInvalidSettings(t *testing.T) {
	t.Parallel()

	values := map[string]string{
		"TELEMETRY_ENABLED":            "false",
		"OTEL_EXPORTER_OTLP_ENDPOINT":  "not a url",
		"TELEMETRY_TRACE_SAMPLE_RATIO": "invalid",
	}
	cfg, err := loadTelemetry(func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	})
	if err != nil {
		t.Fatalf("loadTelemetry() error = %v", err)
	}
	if cfg.Enabled {
		t.Fatal("Telemetry.Enabled = true, want false")
	}
}

func TestTelemetryEnabledLoadsBoundedSettings(t *testing.T) {
	t.Parallel()

	values := map[string]string{
		"TELEMETRY_ENABLED":                     "true",
		"OTEL_EXPORTER_OTLP_ENDPOINT":           "https://collector.example.com/otel",
		"TELEMETRY_EXPORT_TIMEOUT":              "3s",
		"TELEMETRY_RETRY_INITIAL_INTERVAL":      "250ms",
		"TELEMETRY_RETRY_MAX_INTERVAL":          "1s",
		"TELEMETRY_RETRY_MAX_ELAPSED_TIME":      "2s",
		"TELEMETRY_METRIC_INTERVAL":             "20s",
		"TELEMETRY_TRACE_SAMPLE_RATIO":          "0.25",
		"TELEMETRY_TRACE_MAX_QUEUE_SIZE":        "1024",
		"TELEMETRY_TRACE_MAX_EXPORT_BATCH_SIZE": "256",
		"TELEMETRY_TRACE_BATCH_TIMEOUT":         "2s",
		"TELEMETRY_MAX_EXPORT_REQUEST_BYTES":    "2097152",
		"TELEMETRY_SHUTDOWN_TIMEOUT":            "7s",
	}
	cfg, err := loadTelemetry(func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	})
	if err != nil {
		t.Fatalf("loadTelemetry() error = %v", err)
	}
	if !cfg.Enabled {
		t.Fatal("Telemetry.Enabled = false, want true")
	}
	if cfg.TraceSampleRatio != 0.25 {
		t.Fatalf("TraceSampleRatio = %v, want 0.25", cfg.TraceSampleRatio)
	}
	if cfg.ExportTimeout != 3*time.Second {
		t.Fatalf("ExportTimeout = %v, want 3s", cfg.ExportTimeout)
	}
	if cfg.TraceMaxExportBatchSize != 256 {
		t.Fatalf("TraceMaxExportBatchSize = %d, want 256", cfg.TraceMaxExportBatchSize)
	}
}

func TestTelemetryEndpointParseErrorRedactsRawValue(t *testing.T) {
	t.Parallel()

	const secret = "otel-secret-marker"
	cfg := defaultTelemetryConfig()
	cfg.Enabled = true
	cfg.Endpoint = "https://" + secret + "@collector.example.com/%zz"

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want malformed endpoint error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("Validate() error exposed raw endpoint: %q", err.Error())
	}

	var parseErr *url.Error
	if !errors.As(err, &parseErr) {
		t.Fatalf("Validate() error = %T, want wrapped *url.Error", err)
	}
}

func TestTelemetryRejectsUnsafeBounds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*TelemetryConfig)
	}{
		{
			name: "invalid endpoint scheme",
			mutate: func(cfg *TelemetryConfig) {
				cfg.Endpoint = "ftp://collector.example.com"
			},
		},
		{
			name: "endpoint userinfo",
			mutate: func(cfg *TelemetryConfig) {
				cfg.Endpoint = "https://secret@collector.example.com"
			},
		},
		{
			name: "sample ratio",
			mutate: func(cfg *TelemetryConfig) {
				cfg.TraceSampleRatio = 1.1
			},
		},
		{
			name: "batch exceeds queue",
			mutate: func(cfg *TelemetryConfig) {
				cfg.TraceMaxQueueSize = 10
				cfg.TraceMaxExportBatchSize = 11
			},
		},
		{
			name: "retry interval order",
			mutate: func(cfg *TelemetryConfig) {
				cfg.RetryInitialInterval = 2 * time.Second
				cfg.RetryMaxInterval = time.Second
			},
		},
		{
			name: "unbounded request size",
			mutate: func(cfg *TelemetryConfig) {
				cfg.MaxExportRequestBytes = 0
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := defaultTelemetryConfig()
			cfg.Enabled = true
			tt.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("Validate() error = nil, want error")
			}
		})
	}
}
