package telemetry

import (
	"errors"
	"strings"
	"testing"
	"time"
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
