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

func TestHumaUnknownHandlerErrorIsSanitized(t *testing.T) {
	t.Parallel()

	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	api := humachi.New(router, newAPIConfig())

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
