package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Lamy210/go-template/internal/httpmethod"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func TestRequestIDBoundary(t *testing.T) {
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
			handler := requestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotContextID = middleware.GetReqID(r.Context())
				gotHeader = r.Header.Get(middleware.RequestIDHeader)
				w.WriteHeader(http.StatusNoContent)
			}))

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
			if got := res.Header().Get(middleware.RequestIDHeader); got != gotContextID {
				t.Fatalf("response request ID = %q, want context ID %q", got, gotContextID)
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
			if !generatedRequestIDShape(gotContextID) {
				t.Fatalf("generated request ID has unexpected shape: %q", gotContextID)
			}
		})
	}
}

func TestRequestIDGeneratesDistinctIdentifiers(t *testing.T) {
	t.Parallel()

	var ids []string
	handler := requestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ids = append(ids, middleware.GetReqID(r.Context()))
		w.WriteHeader(http.StatusNoContent)
	}))

	for range 2 {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want %d", res.Code, http.StatusNoContent)
		}
	}

	if len(ids) != 2 {
		t.Fatalf("generated request ID count = %d, want 2", len(ids))
	}
	if ids[0] == ids[1] {
		t.Fatalf("generated request IDs are equal: %q", ids[0])
	}
}

func generatedRequestIDShape(value string) bool {
	if len(value) < 26 || len(value) > maxClientRequestIDBytes {
		return false
	}
	for i := 0; i < len(value); i++ {
		if (value[i] >= 'A' && value[i] <= 'Z') ||
			(value[i] >= '2' && value[i] <= '7') {
			continue
		}
		return false
	}
	return true
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

func TestDefaultResponsesDoNotExposeSchemaLinks(t *testing.T) {
	t.Parallel()

	server := newTestServer(
		t,
		testConfig(),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		testServiceInfo(),
		nil,
	)

	req := httptest.NewRequest(http.MethodGet, "/version", nil)
	req.Header.Set("X-Forwarded-Host", "attacker.example")
	req.Header.Set("Forwarded", "host=forwarded-attacker.example")
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf(
			"GET /version status = %d, want %d; body=%s",
			res.Code,
			http.StatusOK,
			res.Body.String(),
		)
	}
	if got := res.Header().Get("Link"); got != "" {
		t.Fatalf("GET /version Link header = %q, want empty", got)
	}
	for _, forbidden := range []string{
		"$schema",
		"attacker.example",
		"forwarded-attacker.example",
	} {
		if strings.Contains(res.Body.String(), forbidden) {
			t.Fatalf("GET /version response contains %q: %s", forbidden, res.Body.String())
		}
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

func TestDocsExposureCanBeDisabled(t *testing.T) {
	t.Parallel()

	cfg := testConfig()
	cfg.DocsEnabled = false
	server := newTestServer(
		t,
		cfg,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		testServiceInfo(),
		nil,
	)

	for _, path := range []string{
		"/docs",
		"/openapi.json",
		"/openapi.yaml",
		"/schemas/HealthOutput.json",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, req)
		if res.Code != http.StatusNotFound {
			t.Fatalf("GET %s status = %d, want %d", path, res.Code, http.StatusNotFound)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf(
			"GET /health/live status = %d, want %d; body=%s",
			res.Code,
			http.StatusOK,
			res.Body.String(),
		)
	}
	if got := res.Header().Get("Link"); got != "" {
		t.Fatalf("GET /health/live Link header = %q, want empty", got)
	}
	if strings.Contains(res.Body.String(), "$schema") {
		t.Fatalf("GET /health/live contains disabled $schema link: %s", res.Body.String())
	}
}

func TestListenBindsConfiguredAddress(t *testing.T) {
	t.Parallel()

	cfg := testConfig()
	cfg.Addr = "127.0.0.1:0"
	server := newTestServer(
		t,
		cfg,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		testServiceInfo(),
		nil,
	)

	listener, err := server.Listen(context.Background())
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	t.Cleanup(func() {
		_ = listener.Close()
	})

	if listener.Addr() == nil {
		t.Fatal("Listen() returned listener without address")
	}
}

func TestListenFailsSynchronouslyWhenAddressIsOccupied(t *testing.T) {
	t.Parallel()

	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupy address: %v", err)
	}
	t.Cleanup(func() {
		_ = occupied.Close()
	})

	cfg := testConfig()
	cfg.Addr = occupied.Addr().String()
	server := newTestServer(
		t,
		cfg,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		testServiceInfo(),
		nil,
	)

	listener, err := server.Listen(context.Background())
	if listener != nil {
		_ = listener.Close()
	}
	if err == nil {
		t.Fatal("Listen() error = nil, want address-in-use error")
	}
}

func TestServerMethodsRejectUninitializedServer(t *testing.T) {
	t.Parallel()

	for _, server := range []*Server{nil, {}} {
		if handler := server.Handler(); handler != nil {
			t.Fatalf("Handler() = %T, want nil", handler)
		}

		listener, err := server.Listen(context.Background())
		if listener != nil {
			_ = listener.Close()
			t.Fatal("Listen() returned listener for uninitialized server")
		}
		if err == nil {
			t.Fatal("Listen() error = nil, want uninitialized server error")
		}

		if err := server.Serve(nil); err == nil {
			t.Fatal("Serve() error = nil, want uninitialized server error")
		}
		if err := server.ListenAndServe(); err == nil {
			t.Fatal("ListenAndServe() error = nil, want uninitialized server error")
		}
		if err := server.Shutdown(context.Background()); err == nil {
			t.Fatal("Shutdown() error = nil, want uninitialized server error")
		}
	}
}

func TestServeRejectsNilListener(t *testing.T) {
	t.Parallel()

	server := newTestServer(
		t,
		testConfig(),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		testServiceInfo(),
		nil,
	)

	if err := server.Serve(nil); err == nil {
		t.Fatal("Serve(nil) error = nil, want error")
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

func TestOuterMiddlewareRuntimePanicReturnsSanitizedInternalError(t *testing.T) {
	t.Parallel()

	const sensitive = "sensitive outer middleware runtime panic"
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	server := newTestServer(
		t,
		testConfig(),
		logger,
		testServiceInfo(),
		nil,
		WithOuterMiddleware(func(http.Handler) http.Handler {
			return http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				panic(sensitive)
			})
		}),
	)

	req := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)

	if res.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusInternalServerError)
	}
	if strings.Contains(res.Body.String(), sensitive) {
		t.Fatalf("response exposed outer middleware panic value: %s", res.Body.String())
	}
	if strings.Contains(logs.String(), sensitive) {
		t.Fatalf("logs exposed outer middleware panic value: %s", logs.String())
	}

	var body struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode panic response: %v; body=%s", err, res.Body.String())
	}
	if body.Code != internalErrorCode || body.Message != "internal server error" {
		t.Fatalf("panic response = %#v", body)
	}
	if body.RequestID == "" {
		t.Fatal("outer middleware panic response request_id is empty")
	}
	if got := res.Header().Get(middleware.RequestIDHeader); got != body.RequestID {
		t.Fatalf("response request ID = %q, want %q", got, body.RequestID)
	}
}

func TestOuterMiddlewareRuntimePanicAbortsStartedResponse(t *testing.T) {
	t.Parallel()

	const sensitive = "sensitive started outer middleware panic"
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	server := newTestServer(
		t,
		testConfig(),
		logger,
		testServiceInfo(),
		nil,
		WithOuterMiddleware(func(http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte("partial"))
				panic(sensitive)
			})
		}),
	)

	req := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	res := httptest.NewRecorder()

	var recovered any
	func() {
		defer func() {
			recovered = recover()
		}()
		server.Handler().ServeHTTP(res, req)
	}()

	err, ok := recovered.(error)
	if !ok || !errors.Is(err, http.ErrAbortHandler) {
		t.Fatalf("recovered = %#v, want http.ErrAbortHandler", recovered)
	}
	if got := res.Body.String(); got != "partial" {
		t.Fatalf("response body = %q, want original partial body only", got)
	}
	if strings.Contains(logs.String(), sensitive) {
		t.Fatalf("logs exposed started outer middleware panic value: %s", logs.String())
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

func TestAccessLogContextAttrsCannotOverrideCanonicalFields(t *testing.T) {
	t.Parallel()

	const traceID = "0123456789abcdef0123456789abcdef"
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil)).With(
		"service", "canonical-service",
	)
	server := newTestServer(
		t,
		testConfig(),
		logger,
		testServiceInfo(),
		nil,
		WithContextLogAttrs(func(context.Context) []slog.Attr {
			return []slog.Attr{
				slog.String("service", "spoofed-service"),
				slog.String("method", "SPOOFED"),
				slog.String("route", "/sensitive/raw/path"),
				slog.Int("status", 999),
				slog.String("request_id", "spoofed-request"),
				slog.String("trace_id", traceID),
			}
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
	if entry["service"] != "canonical-service" {
		t.Fatalf("service = %v, want canonical-service", entry["service"])
	}
	if entry["method"] != http.MethodGet {
		t.Fatalf("method = %v, want %s", entry["method"], http.MethodGet)
	}
	if entry["route"] != "/health/live" {
		t.Fatalf("route = %v, want /health/live", entry["route"])
	}
	if entry["status"] != float64(http.StatusOK) {
		t.Fatalf("status = %v, want %d", entry["status"], http.StatusOK)
	}
	if entry["request_id"] == "spoofed-request" || entry["request_id"] == "" {
		t.Fatalf("request_id = %v, want generated canonical value", entry["request_id"])
	}
	if entry["trace_id"] != traceID {
		t.Fatalf("trace_id = %v, want %s", entry["trace_id"], traceID)
	}
	if strings.Contains(logs.String(), "sensitive/raw/path") ||
		strings.Contains(logs.String(), "spoofed-service") {
		t.Fatalf("access log contains reserved callback value: %s", logs.String())
	}
}

func TestAccessLogContextAttrsLimitAppliesAfterReservedFiltering(t *testing.T) {
	t.Parallel()

	const traceID = "0123456789abcdef0123456789abcdef"
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	server := newTestServer(
		t,
		testConfig(),
		logger,
		testServiceInfo(),
		nil,
		WithContextLogAttrs(func(context.Context) []slog.Attr {
			attrs := make([]slog.Attr, 0, maxContextLogAttrs+2)
			attrs = append(
				attrs,
				slog.String("method", "SPOOFED"),
				slog.String("status", "SPOOFED"),
			)
			for i := 0; i < maxContextLogAttrs-1; i++ {
				attrs = append(
					attrs,
					slog.Int(fmt.Sprintf("context_%02d", i), i),
				)
			}
			attrs = append(attrs, slog.String("trace_id", traceID))
			return attrs
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
	if entry["trace_id"] != traceID {
		t.Fatalf("trace_id = %v, want %s", entry["trace_id"], traceID)
	}
	if entry["method"] != http.MethodGet {
		t.Fatalf("method = %v, want %s", entry["method"], http.MethodGet)
	}
	if entry["status"] != float64(http.StatusOK) {
		t.Fatalf("status = %v, want %d", entry["status"], http.StatusOK)
	}
}

func TestAccessLogContextAttrsAreBounded(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	server := newTestServer(
		t,
		testConfig(),
		logger,
		testServiceInfo(),
		nil,
		WithContextLogAttrs(func(context.Context) []slog.Attr {
			attrs := make([]slog.Attr, 0, maxContextLogAttrs+4)
			for i := 0; i < maxContextLogAttrs+4; i++ {
				attrs = append(
					attrs,
					slog.Int(fmt.Sprintf("context_%02d", i), i),
				)
			}
			return attrs
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
	if _, ok := entry["context_15"]; !ok {
		t.Fatal("last allowed context attribute is missing")
	}
	if _, ok := entry["context_16"]; ok {
		t.Fatalf("context attributes exceeded max %d: %s", maxContextLogAttrs, logs.String())
	}
}

func TestAccessLogContainsRouteObserverPanic(t *testing.T) {
	t.Parallel()

	const sensitive = "sensitive route observer panic"
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	router := chi.NewRouter()
	router.Use(accessLog(
		logger,
		nil,
		func(context.Context, string, string) {
			panic(sensitive)
		},
	))
	router.Get("/observed", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/observed", nil)
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	if res.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusNoContent)
	}
	if strings.Contains(logs.String(), sensitive) {
		t.Fatalf("logs exposed route observer panic value: %s", logs.String())
	}
	if !strings.Contains(logs.String(), "http route observer panic") {
		t.Fatalf("sanitized route observer warning missing: %s", logs.String())
	}
	if !strings.Contains(logs.String(), "http request") {
		t.Fatalf("access log missing after route observer panic: %s", logs.String())
	}
}

func TestAccessLogContainsContextAttrsPanic(t *testing.T) {
	t.Parallel()

	const sensitive = "sensitive context attrs panic"
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	router := chi.NewRouter()
	router.Use(accessLog(
		logger,
		func(context.Context) []slog.Attr {
			panic(sensitive)
		},
		nil,
	))
	router.Get("/observed", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/observed", nil)
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	if res.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusNoContent)
	}
	if strings.Contains(logs.String(), sensitive) {
		t.Fatalf("logs exposed context attrs panic value: %s", logs.String())
	}
	if !strings.Contains(logs.String(), "http context log attributes panic") {
		t.Fatalf("sanitized context attrs warning missing: %s", logs.String())
	}
	if !strings.Contains(logs.String(), "http request") {
		t.Fatalf("access log missing after context attrs panic: %s", logs.String())
	}
}

func TestAccessLogUsesImplicitOKStatus(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	router := chi.NewRouter()
	router.Use(accessLog(logger, nil, nil))
	router.Get("/implicit-ok", func(http.ResponseWriter, *http.Request) {
		// Intentionally do not call WriteHeader or Write. net/http still emits
		// an implicit 200 OK response when the handler returns.
	})

	req := httptest.NewRequest(http.MethodGet, "/implicit-ok", nil)
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	if got := res.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("response status = %d, want %d", got, http.StatusOK)
	}

	var entry map[string]any
	if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
		t.Fatalf("decode access log: %v; log=%s", err, logs.String())
	}
	if got := entry["status"]; got != float64(http.StatusOK) {
		t.Fatalf("access log status = %v, want %d", got, http.StatusOK)
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

func TestAccessLogNormalizesUnknownMethod(t *testing.T) {
	t.Parallel()

	const rawMethod = "BREW-tenant-12345"
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	router := chi.NewRouter()
	router.Use(accessLog(logger, nil, nil))
	router.Get("/custom", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	req := httptest.NewRequest(rawMethod, "/custom", nil)
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	if res.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusMethodNotAllowed)
	}

	var entry map[string]any
	if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
		t.Fatalf("decode access log: %v; log=%s", err, logs.String())
	}
	if entry["method"] != httpmethod.Other {
		t.Fatalf("method = %v, want %q", entry["method"], httpmethod.Other)
	}
	if strings.Contains(logs.String(), rawMethod) {
		t.Fatalf("access log exposed raw unknown method: %s", logs.String())
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
		DocsEnabled:       true,
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
