package messaging

import (
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
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

func TestConsumerConfigRejectsAckWaitWithoutSettlementSlack(t *testing.T) {
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
		AckWait:            2 * time.Second,
		ProcessAttempts:    1,
		QuarantineAttempts: 1,
		MaxAckPending:      1,
		RetryDelay:         time.Second,
		HandlerTimeout:     time.Second,
		AckTimeout:         time.Second,
		PullExpiry:         time.Second,
	}

	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want ack-wait budget error")
	}

	cfg.AckWait = 3 * time.Second
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() with settlement slack error = %v", err)
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

func TestManagedStreamConfigMatchesManagedFields(t *testing.T) {
	t.Parallel()

	desired := StreamConfig{
		Name:            "TEST",
		Subjects:        []string{"test.a", "test.b"},
		MaxConsumers:    2,
		MaxMessages:     100,
		MaxBytes:        4096,
		MaxAge:          time.Hour,
		MaxMessageSize:  1024,
		DuplicateWindow: time.Minute,
	}
	actual := managedStreamConfig(desired)
	actual.Subjects = []string{"test.b", "test.a"}

	if !managedStreamConfigMatches(actual, desired) {
		t.Fatal("managed stream config did not match equivalent subject ordering")
	}

	actual.MaxBytes++
	if managedStreamConfigMatches(actual, desired) {
		t.Fatal("managed stream config matched after max-bytes drift")
	}
}

func TestManagedStreamConfigRejectsUnsafeServerFlags(t *testing.T) {
	t.Parallel()

	desired := StreamConfig{
		Name:            "TEST",
		Subjects:        []string{"test.>"},
		MaxConsumers:    2,
		MaxMessages:     100,
		MaxBytes:        4096,
		MaxAge:          time.Hour,
		MaxMessageSize:  1024,
		DuplicateWindow: time.Minute,
	}

	tests := []struct {
		name   string
		mutate func(*jetstream.StreamConfig)
	}{
		{
			name: "no ack",
			mutate: func(cfg *jetstream.StreamConfig) {
				cfg.NoAck = true
			},
		},
		{
			name: "sealed",
			mutate: func(cfg *jetstream.StreamConfig) {
				cfg.Sealed = true
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			actual := managedStreamConfig(desired)
			tt.mutate(&actual)
			if managedStreamConfigMatches(actual, desired) {
				t.Fatal("managed stream config matched unsafe server state")
			}
		})
	}
}
