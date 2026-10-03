package outbox

import (
	"testing"
	"time"
)

func TestRetryDelayForEventNeverDropsBelowBaseAfterFirstAttempt(t *testing.T) {
	t.Parallel()

	const (
		base    = 20 * time.Hour
		maximum = 24 * time.Hour
	)

	got := retryDelayForEvent("event-2", 2, base, maximum)
	if got < base {
		t.Fatalf("second retry delay = %v, want at least base %v", got, base)
	}
	if got > maximum {
		t.Fatalf("second retry delay = %v, want at most maximum %v", got, maximum)
	}
}
