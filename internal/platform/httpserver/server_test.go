package httpserver

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Lamy210/go-template/internal/config"
)

func TestHealthEndpoints(t *testing.T) {
	t.Parallel()

	server := New(testConfig(), slog.New(slog.NewTextHandler(io.Discard, nil)))

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

func TestOpenAPIEndpoint(t *testing.T) {
	t.Parallel()

	server := New(testConfig(), slog.New(slog.NewTextHandler(io.Discard, nil)))
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
