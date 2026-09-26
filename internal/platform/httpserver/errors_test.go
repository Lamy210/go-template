package httpserver

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Lamy210/go-template/internal/core/apperror"
)

func TestToHTTPErrorMapsApplicationError(t *testing.T) {
	t.Parallel()

	err := apperror.New(
		apperror.KindNotFound,
		"widget_not_found",
		"widget was not found",
		apperror.Detail{Field: "widget_id", Code: "not_found", Message: "unknown widget"},
	)

	mapped := toHTTPError(context.Background(), err)
	response, ok := mapped.(*errorResponse)
	if !ok {
		t.Fatalf("toHTTPError() type = %T, want *errorResponse", mapped)
	}

	if response.GetStatus() != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.GetStatus(), http.StatusNotFound)
	}
	if response.Code != "widget_not_found" {
		t.Fatalf("code = %q", response.Code)
	}
	if response.Message != "widget was not found" {
		t.Fatalf("message = %q", response.Message)
	}
	if len(response.Details) != 1 || response.Details[0].Field != "widget_id" {
		t.Fatalf("details = %#v", response.Details)
	}
}

func TestToHTTPErrorFindsWrappedApplicationError(t *testing.T) {
	t.Parallel()

	appErr := apperror.New(apperror.KindConflict, "version_conflict", "resource changed")
	err := errors.Join(errors.New("additional context"), appErr)

	mapped := toHTTPError(context.Background(), err)
	response := mapped.(*errorResponse)

	if response.GetStatus() != http.StatusConflict {
		t.Fatalf("status = %d, want %d", response.GetStatus(), http.StatusConflict)
	}
	if response.Code != "version_conflict" {
		t.Fatalf("code = %q", response.Code)
	}
}

func TestToHTTPErrorHidesUnknownError(t *testing.T) {
	t.Parallel()

	const secret = "postgres://user:password@db/private"
	mapped := toHTTPError(context.Background(), errors.New(secret))
	response := mapped.(*errorResponse)

	if response.GetStatus() != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.GetStatus(), http.StatusInternalServerError)
	}
	if response.Code != internalErrorCode {
		t.Fatalf("code = %q, want %q", response.Code, internalErrorCode)
	}
	if strings.Contains(response.Error(), secret) {
		t.Fatalf("response exposed internal error: %q", response.Error())
	}
}
