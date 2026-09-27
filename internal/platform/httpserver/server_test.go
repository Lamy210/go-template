package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

func TestSanitizeRequestIDBoundary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		requestID string
		wantKeep  bool
	}{
		{
			name:      "valid upstream id",
			requestID: "upstream-123_ABC/xyz",
			wantKeep:  true,
		},
		{
			name:      "maximum length",
			requestID: strings.Repeat("a", maxClientRequestIDBytes),
			wantKeep:  true,
		},
		{
			name:      "too long",
			requestID: strings.Repeat("a", maxClientRequestIDBytes+1),
			wantKeep:  false,
		},
		{
			name:      "contains whitespace",
			requestID: "request id with spaces",
			wantKeep:  false,
		},
		{
			name:      "unicode",
			requestID: "雪",
			wantKeep:  false,
		},
		{
			name:     "missing",
			wantKeep: false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var gotContextID string
			var gotHeader string
			handler := sanitizeRequestID(
				middleware.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					gotContextID = middleware.GetReqID(r.Context())
					gotHeader = r.Header.Get(middleware.RequestIDHeader)
					w.WriteHeader(http.StatusNoContent)
				})),
			)

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.requestID != "" {
				req.Header.Set(middleware.RequestIDHeader, tt.requestID)
			}
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)

			if res.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want %d", res.Code, http.StatusNoContent)
			}
			if gotContextID == "" {
				t.Fatal("request ID context is empty")
			}

			if tt.wantKeep {
				if gotContextID != tt.requestID {
					t.Fatalf("context request ID = %q, want %q", gotContextID, tt.requestID)
				}
				if gotHeader != tt.requestID {
					t.Fatalf("request header = %q, want %q", gotHeader, tt.requestID)
				}
				return
			}

			if tt.requestID != "" && gotContextID == tt.requestID {
				t.Fatalf("invalid client request ID was trusted: %q", gotContextID)
			}
			if gotHeader != "" {
				t.Fatalf("invalid request ID header was not removed: %q", gotHeader)
			}
		})
	}
}

func TestHealthEndpoints(t *testing.T) {
	t.Parallel()

	server := newTestServer(t,
		testConfig(),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		testServiceInfo(),
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

	server := newTestServer(t,
		testConfig(),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		testServiceInfo(),
		ready,
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

	server := newTestServer(t,
		testConfig(),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		testServiceInfo(),
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

	server := newTestServer(t,
		testConfig(),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		testServiceInfo(),
		nil,
	)
	req := httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("GET /openapi.json status = %d, want %d; body=%s", res.Code, http.StatusOK, res.Body.String())
	}
}

func TestShutdownForceClosesActiveConnectionsAfterDeadline(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	requestCanceled := make(chan struct{})
	release := make(chan struct{})
	t.Cleanup(func() {
		close(release)
	})

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-r.Context().Done():
			close(requestCanceled)
		case <-release:
		}
	})

	server := &Server{
		handler: handler,
		httpServer: &http.Server{
			Handler:           handler,
			ReadHeaderTimeout: time.Second,
			ReadTimeout:       time.Second,
			WriteTimeout:      time.Second,
			IdleTimeout:       time.Second,
		},
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.httpServer.Serve(listener)
	}()

	client := &http.Client{Timeout: 2 * time.Second}
	clientErr := make(chan error, 1)
	go func() {
		resp, err := client.Get("http://" + listener.Addr().String())
		if resp != nil {
			_ = resp.Body.Close()
		}
		clientErr <- err
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("request handler did not start")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	err = server.Shutdown(shutdownCtx)
	cancel()

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown() error = %v, want deadline exceeded", err)
	}

	select {
	case <-requestCanceled:
	case <-time.After(time.Second):
		t.Fatal("active request context was not canceled by force close")
	}

	select {
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("Serve() error = %v, want http.ErrServerClosed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("HTTP server did not stop")
	}

	select {
	case <-clientErr:
	case <-time.After(time.Second):
		t.Fatal("HTTP client did not observe connection shutdown")
	}
}

func TestAccessLogIncludesContextAttributes(t *testing.T) {
	t.Parallel()

	type correlationKey struct{}
	const correlationValue = "0123456789abcdef0123456789abcdef"

	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	server := newTestServer(t,
		testConfig(),
		logger,
		testServiceInfo(),
		nil,
		WithContextLogAttrs(func(ctx context.Context) []slog.Attr {
			value, _ := ctx.Value(correlationKey{}).(string)
			if value == "" {
				return nil
			}
			return []slog.Attr{slog.String("trace_id", value)}
		}),
		WithOuterMiddleware(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ctx := context.WithValue(r.Context(), correlationKey{}, correlationValue)
				next.ServeHTTP(w, r.WithContext(ctx))
			})
		}),
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

func TestAccessLogUsesResolvedRoutePattern(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	router := chi.NewRouter()
	router.Use(accessLog(logger, nil, nil))
	router.Get("/widgets/{widgetID}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/widgets/12345", nil)
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	if res.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusNoContent)
	}

	var entry map[string]any
	if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
		t.Fatalf("decode access log: %v; log=%s", err, logs.String())
	}
	if entry["route"] != "/widgets/{widgetID}" {
		t.Fatalf("route = %v, want route template", entry["route"])
	}
	if _, exists := entry["path"]; exists {
		t.Fatalf("access log contains raw path field: %s", logs.String())
	}
	if strings.Contains(logs.String(), "12345") {
		t.Fatalf("access log exposed raw path parameter: %s", logs.String())
	}
}

func TestAccessLogDoesNotLogUnmatchedRawPath(t *testing.T) {
	t.Parallel()

	const sensitivePath = "/not-registered/secret-value-12345"
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	router := chi.NewRouter()
	router.Use(accessLog(logger, nil, nil))
	// chi bypasses middleware entirely when a mux has no registered routes.
	// Register an unrelated route so this test matches the production router.
	router.Get("/registered", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, sensitivePath, nil)
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	if res.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusNotFound)
	}

	var entry map[string]any
	if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
		t.Fatalf("decode access log: %v; log=%s", err, logs.String())
	}
	if entry["route"] != unmatchedRoute {
		t.Fatalf("route = %v, want %q", entry["route"], unmatchedRoute)
	}
	if _, exists := entry["path"]; exists {
		t.Fatalf("access log contains raw path field: %s", logs.String())
	}
	if strings.Contains(logs.String(), sensitivePath) ||
		strings.Contains(logs.String(), "secret-value-12345") {
		t.Fatalf("access log exposed unmatched raw path: %s", logs.String())
	}
}

func TestRouteObserverUsesResolvedChiPattern(t *testing.T) {
	t.Parallel()

	var gotMethod string
	var gotRoute string

	router := chi.NewRouter()
	router.Use(accessLog(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		nil,
		func(_ context.Context, method, route string) {
			gotMethod = method
			gotRoute = route
		},
	))
	router.Get("/widgets/{widgetID}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/widgets/12345", nil)
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	if res.Code != http.StatusNoContent {
		t.Fatalf("GET /widgets/12345 status = %d, want %d", res.Code, http.StatusNoContent)
	}
	if gotMethod != http.MethodGet {
		t.Fatalf("route observer method = %q, want %q", gotMethod, http.MethodGet)
	}
	if gotRoute != "/widgets/{widgetID}" {
		t.Fatalf("route observer route = %q, want %q", gotRoute, "/widgets/{widgetID}")
	}
	if gotRoute == req.URL.Path {
		t.Fatalf("route observer used raw path %q instead of route pattern", gotRoute)
	}
}

func TestRouteObserverIgnoresUnmatchedRoute(t *testing.T) {
	t.Parallel()

	called := false
	router := chi.NewRouter()
	router.Use(accessLog(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		nil,
		func(context.Context, string, string) {
			called = true
		},
	))

	req := httptest.NewRequest(http.MethodGet, "/not-registered/12345", nil)
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	if res.Code != http.StatusNotFound {
		t.Fatalf("unmatched route status = %d, want %d", res.Code, http.StatusNotFound)
	}
	if called {
		t.Fatal("route observer was called for unmatched raw path")
	}
}

func newTestServer(
	t *testing.T,
	cfg Config,
	logger *slog.Logger,
	info ServiceInfo,
	ready ReadinessCheck,
	opts ...Option,
) *Server {
	t.Helper()

	server, err := New(cfg, logger, info, ready, opts...)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return server
}

func testConfig() Config {
	return Config{
		Addr:              ":0",
		ReadHeaderTimeout: time.Second,
		ReadTimeout:       time.Second,
		WriteTimeout:      time.Second,
		IdleTimeout:       time.Second,
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
