package safeerror

import (
	"errors"
	"strings"
	"testing"
)

type typedCause struct {
	detail string
}

func (e *typedCause) Error() string {
	return e.detail
}

func TestWrapRetainsCauseWithoutExposingIt(t *testing.T) {
	t.Parallel()

	cause := &typedCause{detail: "opaque dependency diagnostic with secret"}
	err := Wrap("query dependency", cause)

	if !errors.Is(err, cause) {
		t.Fatal("Wrap() did not retain cause for errors.Is")
	}
	if got, ok := errors.AsType[*typedCause](err); !ok || got != cause {
		t.Fatalf("errors.AsType() = (%v, %t), want original typed cause", got, ok)
	}
	if got := err.Error(); got != "query dependency" {
		t.Fatalf("Error() = %q, want safe operation", got)
	}
	if strings.Contains(err.Error(), cause.Error()) {
		t.Fatalf("Error() exposed raw cause: %q", err.Error())
	}
}

func TestWrapNilCauseReturnsNil(t *testing.T) {
	t.Parallel()

	if err := Wrap("query dependency", nil); err != nil {
		t.Fatalf("Wrap(nil cause) = %v, want nil", err)
	}
}
