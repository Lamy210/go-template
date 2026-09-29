package httpserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Lamy210/go-template/internal/httpmethod"
	"github.com/go-chi/chi/v5/middleware"
)

func TestSafeRecovererReturnsSanitizedInternalError(t *testing.T) {
	t.Parallel()

	const sensitive = "secret-bearing panic value"
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))

	var handler http.Handler = http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(sensitive)
	})
	handler = safeRecoverer(logger)(handler)
	handler = accessLog(logger, nil, nil)(handler)
	handler = middleware.RequestID(handler)

	req := httptest.NewRequest(http.MethodGet, "/panic", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusInternalServerError)
	}
	if got := res.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	if strings.Contains(res.Body.String(), sensitive) {
		t.Fatalf("response exposed panic value: %s", res.Body.String())
	}
	if strings.Contains(logs.String(), sensitive) {
		t.Fatalf("logs exposed panic value: %s", logs.String())
	}

	var body decodedErrorResponse
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error response: %v; body=%s", err, res.Body.String())
	}
	if body.Code != internalErrorCode {
		t.Fatalf("code = %q, want %q", body.Code, internalErrorCode)
	}
	if body.Message != "internal server error" {
		t.Fatalf("message = %q, want internal server error", body.Message)
	}
	if body.RequestID == "" {
		t.Fatal("request_id is empty")
	}
	if !strings.Contains(logs.String(), "http handler panic") {
		t.Fatalf("panic marker missing from logs: %s", logs.String())
	}
	if !strings.Contains(logs.String(), "\"status\":500") {
		t.Fatalf("access log did not record recovered 500: %s", logs.String())
	}
}

func TestSafeRecovererNormalizesUnknownMethodInPanicLog(t *testing.T) {
	t.Parallel()

	const rawMethod = "BREW-sensitive-tenant-12345"
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))

	handler := safeRecoverer(logger)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("opaque panic")
	}))

	req := httptest.NewRequest(rawMethod, "/panic", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusInternalServerError)
	}
	if strings.Contains(logs.String(), rawMethod) {
		t.Fatalf("panic log exposed raw unknown method: %s", logs.String())
	}

	var entry map[string]any
	if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
		t.Fatalf("decode panic log: %v; log=%s", err, logs.String())
	}
	if entry["method"] != httpmethod.Other {
		t.Fatalf("method = %v, want %q", entry["method"], httpmethod.Other)
	}
}

func TestSafeRecovererPreservesAbortHandler(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	handler := safeRecoverer(logger)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))

	req := httptest.NewRequest(http.MethodGet, "/abort", nil)
	res := httptest.NewRecorder()

	var recovered any
	func() {
		defer func() {
			recovered = recover()
		}()
		handler.ServeHTTP(res, req)
	}()

	err, ok := recovered.(error)
	if !ok || !errors.Is(err, http.ErrAbortHandler) {
		t.Fatalf("recovered = %#v, want http.ErrAbortHandler", recovered)
	}
	if logs.Len() != 0 {
		t.Fatalf("abort handler panic was logged: %s", logs.String())
	}
}

func TestSafeRecovererAbortsStartedResponse(t *testing.T) {
	t.Parallel()

	const sensitive = "partial-response panic secret"
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	handler := safeRecoverer(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("partial"))
		panic(sensitive)
	}))

	req := httptest.NewRequest(http.MethodGet, "/partial", nil)
	base := httptest.NewRecorder()
	wrapped := middleware.NewWrapResponseWriter(base, req.ProtoMajor)

	var recovered any
	func() {
		defer func() {
			recovered = recover()
		}()
		handler.ServeHTTP(wrapped, req)
	}()

	err, ok := recovered.(error)
	if !ok || !errors.Is(err, http.ErrAbortHandler) {
		t.Fatalf("recovered = %#v, want http.ErrAbortHandler", recovered)
	}
	if got := base.Body.String(); got != "partial" {
		t.Fatalf("response body = %q, want only original partial body", got)
	}
	if strings.Contains(logs.String(), sensitive) {
		t.Fatalf("logs exposed panic value: %s", logs.String())
	}
}
