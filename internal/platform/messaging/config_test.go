package messaging

import (
	"testing"
	"time"
)

func TestClientConfigRejectsUnlimitedReconnects(t *testing.T) {
	t.Parallel()

	cfg := ClientConfig{
		URL:            "nats://127.0.0.1:4222",
		Name:           "test",
		ConnectTimeout: time.Second,
		ReconnectWait:  time.Second,
		MaxReconnects:  -1,
		DrainTimeout:   time.Second,
		RequestTimeout: time.Second,
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want error")
	}
}

func TestStreamConfigRejectsUnlimitedBounds(t *testing.T) {
	t.Parallel()

	cfg := StreamConfig{
		Name:            "TEST",
		Subjects:        []string{"test.>"},
		MaxConsumers:    1,
		MaxMessages:     -1,
		MaxBytes:        1024,
		MaxAge:          time.Hour,
		MaxMessageSize:  1024,
		DuplicateWindow: time.Minute,
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want error")
	}
}

func TestConsumerConfigRejectsUnboundedDelivery(t *testing.T) {
	t.Parallel()

	cfg := ConsumerConfig{
		Stream: StreamConfig{
			Name:            "TEST",
			Subjects:        []string{"test.>"},
			MaxConsumers:    1,
			MaxMessages:     100,
			MaxBytes:        1024,
			MaxAge:          time.Hour,
			MaxMessageSize:  1024,
			DuplicateWindow: time.Minute,
		},
		Durable:            "worker",
		FilterSubject:      "test.work",
		QuarantineSubject:  "test.quarantine",
		AckWait:            time.Second,
		ProcessAttempts:    0,
		QuarantineAttempts: 1,
		MaxAckPending:      1,
		RetryDelay:         time.Second,
		HandlerTimeout:     time.Second,
		AckTimeout:         time.Second,
		PullExpiry:         time.Second,
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want error")
	}
}

func TestConsumerConfigRejectsDeliveryAttemptOverflow(t *testing.T) {
	t.Parallel()

	cfg := ConsumerConfig{
		Stream: StreamConfig{
			Name:            "TEST",
			Subjects:        []string{"test.>"},
			MaxConsumers:    1,
			MaxMessages:     100,
			MaxBytes:        1024,
			MaxAge:          time.Hour,
			MaxMessageSize:  1024,
			DuplicateWindow: time.Minute,
		},
		Durable:            "worker",
		FilterSubject:      "test.work",
		QuarantineSubject:  "test.quarantine",
		AckWait:            time.Second,
		ProcessAttempts:    int(^uint(0) >> 1),
		QuarantineAttempts: 1,
		MaxAckPending:      1,
		RetryDelay:         time.Second,
		HandlerTimeout:     time.Second,
		AckTimeout:         time.Second,
		PullExpiry:         time.Second,
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want overflow error")
	}
}
