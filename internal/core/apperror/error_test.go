package apperror

import (
	"errors"
	"strings"
	"testing"
)

func TestWrapRetainsCauseWithoutExposingIt(t *testing.T) {
	t.Parallel()

	cause := errors.New("database password=secret")
	err := Wrap(cause, KindUnavailable, "dependency_unavailable", "service temporarily unavailable")

	if !errors.Is(err, cause) {
		t.Fatal("wrapped error does not retain cause")
	}
	if strings.Contains(err.Error(), "password=secret") {
		t.Fatalf("Error() exposed cause: %q", err.Error())
	}
	if got := err.Error(); got != "dependency_unavailable: service temporarily unavailable" {
		t.Fatalf("Error() = %q", got)
	}
}

func TestDetailsReturnsCopy(t *testing.T) {
	t.Parallel()

	err := New(
		KindInvalidArgument,
		"invalid_input",
		"request is invalid",
		Detail{Field: "name", Code: "required", Message: "name is required"},
	)

	details := err.Details()
	details[0].Message = "mutated"

	if got := err.Details()[0].Message; got != "name is required" {
		t.Fatalf("stored detail mutated through returned slice: %q", got)
	}
}

func TestNilErrorAccessorsAreSafe(t *testing.T) {
	t.Parallel()

	var err *Error
	if err.Unwrap() != nil {
		t.Fatal("nil Error.Unwrap() must return nil")
	}
	if err.Kind() != KindInternal {
		t.Fatalf("nil Error.Kind() = %q, want %q", err.Kind(), KindInternal)
	}
	if err.Error() != "<nil>" {
		t.Fatalf("nil Error.Error() = %q", err.Error())
	}
}
