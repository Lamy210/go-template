package messaging

import (
	"testing"
	"time"
)

func TestStreamConfigRejectsDuplicateWindowLongerThanMaxAge(t *testing.T) {
	t.Parallel()

	cfg := StreamConfig{
		Name:            "TEST",
		Subjects:        []string{"test.>"},
		MaxConsumers:    1,
		MaxMessages:     100,
		MaxBytes:        1 << 20,
		MaxAge:          time.Minute,
		MaxMessageSize:  1024,
		DuplicateWindow: 2 * time.Minute,
	}

	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want duplicate-window/max-age error")
	}
}

func TestStreamConfigAllowsDuplicateWindowEqualToMaxAge(t *testing.T) {
	t.Parallel()

	cfg := StreamConfig{
		Name:            "TEST",
		Subjects:        []string{"test.>"},
		MaxConsumers:    1,
		MaxMessages:     100,
		MaxBytes:        1 << 20,
		MaxAge:          time.Minute,
		MaxMessageSize:  1024,
		DuplicateWindow: time.Minute,
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want equality accepted", err)
	}
}
