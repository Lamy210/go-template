package database

import (
	"errors"
	"strings"
	"testing"
)

func TestOperationErrorRetainsCauseWithoutExposingIt(t *testing.T) {
	t.Parallel()

	cause := errors.New("opaque driver diagnostic with sensitive value")
	err := newOperationError("ping postgres", cause)

	if !errors.Is(err, cause) {
		t.Fatal("operation error does not retain cause")
	}
	if strings.Contains(err.Error(), cause.Error()) {
		t.Fatalf("Error() exposed raw dependency error: %q", err.Error())
	}
	if err.Error() != "ping postgres" {
		t.Fatalf("Error() = %q, want %q", err.Error(), "ping postgres")
	}
}
