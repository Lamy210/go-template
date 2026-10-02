package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type decodedErrorResponse struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

func TestHumaValidationErrorUsesCommonContract(t *testing.T) {
	t.Parallel()

	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	api := humachi.New(router, newAPIConfig(true))

	type input struct {
		Name string `query:"name" minLength:"3"`
	}
	type output struct {
		Body struct {
			OK bool `json:"ok"`
		}
	}

	huma.Register(api, huma.Operation{
		OperationID: "test-validation-error",
		Method:      http.MethodGet,
		Path:        "/test-validation-error",
	}, func(context.Context, *input) (*output, error) {
		return &output{}, nil
	})

	req := httptest.NewRequest(http.MethodGet, "/test-validation-error?name=x", nil)
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	if res.Code < 400 || res.Code >= 500 {
		t.Fatalf("status = %d, want 4xx; body=%s", res.Code, res.Body.String())
	}

	body := decodeErrorResponse(t, res)
	if body.Code != "invalid_request" {
		t.Fatalf("code = %q, want invalid_request", body.Code)
	}
	if body.RequestID == "" {
		t.Fatal("request_id is empty")
	}
}

func TestRegisterOperationUsesConfiguredBodyLimitAboveHumaDefault(t *testing.T) {
	t.Parallel()

	const maxBodyBytes = int64(2 << 20)

	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	router.Use(middleware.RequestSize(maxBodyBytes))
	api := humachi.New(router, newAPIConfig(true))

	type input struct {
		Body struct {
			Data string `json:"data"`
		}
	}
	type output struct {
		Body struct {
			Size int `json:"size"`
		}
	}

	var handled bool
	registerOperation(api, maxBodyBytes, huma.Operation{
		OperationID: "test-large-body",
		Method:      http.MethodPost,
		Path:        "/test-large-body",
	}, func(_ context.Context, in *input) (*output, error) {
		handled = true
		out := &output{}
		out.Body.Size = len(in.Body.Data)
		return out, nil
	})

	payload := `{"data":"` + strings.Repeat("a", (1<<20)+1024) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/test-large-body", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", res.Code, http.StatusOK, res.Body.String())
	}
	if !handled {
		t.Fatal("handler was not called for body within configured global limit")
	}
}

func TestBoundedOperationBodyLimit(t *testing.T) {
	t.Parallel()

	const global = int64(1024)
	for _, tt := range []struct {
		name      string
		operation int64
		want      int64
	}{
		{name: "unset uses global", operation: 0, want: global},
		{name: "unlimited is capped", operation: -1, want: global},
		{name: "larger is capped", operation: 2048, want: global},
		{name: "smaller is preserved", operation: 512, want: 512},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := boundedOperationBodyLimit(tt.operation, global); got != tt.want {
				t.Fatalf("boundedOperationBodyLimit(%d, %d) = %d, want %d", tt.operation, global, got, tt.want)
			}
		})
	}
}

func TestHumaUnknownHandlerErrorIsSanitized(t *testing.T) {
	t.Parallel()

	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	api := humachi.New(router, newAPIConfig(true))

	type output struct {
		Body struct {
			OK bool `json:"ok"`
		}
	}

	const internalDetail = "opaque internal handler diagnostic"
	huma.Register(api, huma.Operation{
		OperationID: "test-unknown-error",
		Method:      http.MethodGet,
		Path:        "/test-unknown-error",
	}, func(context.Context, *struct{}) (*output, error) {
		return nil, errors.New(internalDetail)
	})

	req := httptest.NewRequest(http.MethodGet, "/test-unknown-error", nil)
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	if res.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body=%s", res.Code, http.StatusInternalServerError, res.Body.String())
	}
	if strings.Contains(res.Body.String(), internalDetail) {
		t.Fatalf("response exposed internal error: %s", res.Body.String())
	}

	body := decodeErrorResponse(t, res)
	if body.Code != internalErrorCode {
		t.Fatalf("code = %q, want %q", body.Code, internalErrorCode)
	}
	if body.Message != "internal server error" {
		t.Fatalf("message = %q, want internal server error", body.Message)
	}
	if body.RequestID == "" {
		t.Fatal("request_id is empty")
	}
}

func decodeErrorResponse(t *testing.T, res *httptest.ResponseRecorder) decodedErrorResponse {
	t.Helper()

	var body decodedErrorResponse
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error response: %v; body=%s", err, res.Body.String())
	}
	return body
}
