package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Lamy210/go-template/internal/config"
)

func TestHealthEndpoints(t *testing.T) {
	t.Parallel()

	server := New(
		testConfig(),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		testServiceInfo(),
		nil,
		nil,
	)

	for _, path := range []string{"/health/live", "/health/ready"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodGet, path, nil)
			res := httptest.NewRecorder()
			server.Handler().ServeHTTP(res, req)

			if res.Code != http.StatusOK {
				t.Fatalf("GET %s status = %d, want %d; body=%s", path, res.Code, http.StatusOK, res.Body.String())
			}
		})
	}
}

func TestReadinessFailureUsesSafeErrorContract(t *testing.T) {
	t.Parallel()

	const internalDetail = "opaque internal readiness diagnostic"
	ready := func(context.Context) error {
		return errors.New(internalDetail)
	}

	server := New(
		testConfig(),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		testServiceInfo(),
		ready,
		nil,
	)
	req := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)

	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET /health/ready status = %d, want %d; body=%s", res.Code, http.StatusServiceUnavailable, res.Body.String())
	}
	if strings.Contains(res.Body.String(), internalDetail) {
		t.Fatalf("readiness response exposed internal error: %s", res.Body.String())
	}

	var body struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode readiness error: %v; body=%s", err, res.Body.String())
	}
	if body.Code != "service_not_ready" {
		t.Fatalf("code = %q, want service_not_ready", body.Code)
	}
	if body.Message != "service not ready" {
		t.Fatalf("message = %q, want service not ready", body.Message)
	}
	if body.RequestID == "" {
		t.Fatal("request_id is empty")
	}
}

func TestVersionEndpoint(t *testing.T) {
	t.Parallel()

	server := New(
		testConfig(),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		testServiceInfo(),
		nil,
		nil,
	)
	req := httptest.NewRequest(http.MethodGet, "/version", nil)
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("GET /version status = %d, want %d; body=%s", res.Code, http.StatusOK, res.Body.String())
	}

	var body ServiceInfo
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode version response: %v; body=%s", err, res.Body.String())
	}
	want := testServiceInfo()
	if body != want {
		t.Fatalf("version response = %#v, want %#v", body, want)
	}
}

func TestOpenAPIEndpoint(t *testing.T) {
	t.Parallel()

	server := New(
		testConfig(),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		testServiceInfo(),
		nil,
		nil,
	)
	req := httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("GET /openapi.json status = %d, want %d; body=%s", res.Code, http.StatusOK, res.Body.String())
	}
}

func TestAccessLogIncludesContextAttributes(t *testing.T) {
	t.Parallel()

	type correlationKey struct{}
	const correlationValue = "0123456789abcdef0123456789abcdef"

	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	server := New(
		testConfig(),
		logger,
		testServiceInfo(),
		nil,
		func(ctx context.Context) []slog.Attr {
			value, _ := ctx.Value(correlationKey{}).(string)
			if value == "" {
				return nil
			}
			return []slog.Attr{slog.String("trace_id", value)}
		},
		func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ctx := context.WithValue(r.Context(), correlationKey{}, correlationValue)
				next.ServeHTTP(w, r.WithContext(ctx))
			})
		},
	)

	req := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("GET /health/live status = %d, want %d", res.Code, http.StatusOK)
	}

	var entry map[string]any
	if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
		t.Fatalf("decode access log: %v; log=%s", err, logs.String())
	}
	if entry["trace_id"] != correlationValue {
		t.Fatalf("trace_id = %v", entry["trace_id"])
	}
	if entry["request_id"] == "" {
		t.Fatal("request_id is empty")
	}
}

func testConfig() config.HTTPConfig {
	return config.HTTPConfig{
		Addr:              ":0",
		ReadHeaderTimeout: time.Second,
		ReadTimeout:       time.Second,
		WriteTimeout:      time.Second,
		IdleTimeout:       time.Second,
		ShutdownTimeout:   time.Second,
		MaxHeaderBytes:    1 << 20,
		MaxBodyBytes:      1 << 20,
	}
}

func testServiceInfo() ServiceInfo {
	return ServiceInfo{
		Service:   "test-service",
		Version:   "1.2.3",
		Commit:    "abc123",
		BuildTime: "2026-09-27T00:00:00Z",
	}
}
