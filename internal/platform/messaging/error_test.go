package messaging

import (
	"errors"
	"strings"
	"testing"
)

func TestOperationErrorRetainsCauseWithoutExposingIt(t *testing.T) {
	t.Parallel()

	cause := errors.New("opaque broker diagnostic with sensitive value")
	err := newOperationError("connect nats", cause)

	if !errors.Is(err, cause) {
		t.Fatal("operation error does not retain cause")
	}
	if strings.Contains(err.Error(), cause.Error()) {
		t.Fatalf("Error() exposed raw dependency error: %q", err.Error())
	}
	if err.Error() != "connect nats" {
		t.Fatalf("Error() = %q, want connect nats", err.Error())
	}
}
