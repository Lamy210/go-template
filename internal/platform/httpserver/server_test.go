package httpserver

import (
	"context"
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

	server := New(testConfig(), slog.New(slog.NewTextHandler(io.Discard, nil)), nil)

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

func TestReadinessFailureDoesNotExposeInternalError(t *testing.T) {
	t.Parallel()

	const internalDetail = "database connection failed: password=secret"
	ready := func(context.Context) error {
		return errors.New(internalDetail)
	}

	server := New(testConfig(), slog.New(slog.NewTextHandler(io.Discard, nil)), ready)
	req := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)

	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET /health/ready status = %d, want %d; body=%s", res.Code, http.StatusServiceUnavailable, res.Body.String())
	}
	if strings.Contains(res.Body.String(), internalDetail) {
		t.Fatalf("readiness response exposed internal error: %s", res.Body.String())
	}
}

func TestOpenAPIEndpoint(t *testing.T) {
	t.Parallel()

	server := New(testConfig(), slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	req := httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("GET /openapi.json status = %d, want %d; body=%s", res.Code, http.StatusOK, res.Body.String())
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
