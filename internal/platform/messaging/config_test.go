package messaging

import (
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

func TestClientConfigRejectsBlankConnectionIdentity(t *testing.T) {
	t.Parallel()

	base := ClientConfig{
		URL:            "nats://127.0.0.1:4222",
		Name:           "test",
		ConnectTimeout: time.Second,
		ReconnectWait:  time.Second,
		MaxReconnects:  1,
		DrainTimeout:   time.Second,
		RequestTimeout: time.Second,
	}

	tests := []struct {
		name   string
		mutate func(*ClientConfig)
	}{
		{
			name: "blank URL",
			mutate: func(cfg *ClientConfig) {
				cfg.URL = " 	 "
			},
		},
		{
			name: "blank name",
			mutate: func(cfg *ClientConfig) {
				cfg.Name = "\n"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := base
			tt.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("Validate() error = nil, want blank-value error")
			}
		})
	}
}

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

func TestStreamConfigRejectsInvalidName(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		"TEST.EVENTS",
		"TEST\x00EVENTS",
		"TEST\vEVENTS",
		"TEST\u00a0EVENTS",
		string([]byte{0xff}),
	} {
		cfg := testConsumerConfig().Stream
		cfg.Name = name
		if err := cfg.Validate(); err == nil {
			t.Fatalf("Validate(%q) error = nil, want invalid stream name error", name)
		}
	}
}

func TestConsumerConfigRejectsInvalidDurableName(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		"worker/name",
		"worker\x00name",
		"worker\vname",
		"worker\u200bname",
		string([]byte{0xff}),
	} {
		cfg := testConsumerConfig()
		cfg.Durable = name
		if err := cfg.Validate(); err == nil {
			t.Fatalf("Validate(%q) error = nil, want invalid durable name error", name)
		}
	}
}

func TestStreamConfigRejectsInvalidSubjectPattern(t *testing.T) {
	t.Parallel()

	for _, subject := range []string{
		"events.>.invalid",
		string([]byte{0xff}),
		"events." + string([]byte{0xc3, 0x28}),
	} {
		cfg := testConsumerConfig().Stream
		cfg.Subjects = []string{subject}
		if err := cfg.Validate(); err == nil {
			t.Fatalf("Validate(%q) error = nil, want invalid subject pattern error", subject)
		}
	}
}

func TestConsumerConfigRejectsQuarantineRecapture(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*ConsumerConfig)
	}{
		{
			name: "wildcard filter captures quarantine",
			mutate: func(cfg *ConsumerConfig) {
				cfg.FilterSubject = "events.>"
			},
		},
		{
			name: "quarantine subject is wildcard",
			mutate: func(cfg *ConsumerConfig) {
				cfg.QuarantineSubject = "events.*"
			},
		},
		{
			name: "invalid filter wildcard placement",
			mutate: func(cfg *ConsumerConfig) {
				cfg.FilterSubject = "events.>.work"
			},
		},
		{
			name: "invalid UTF-8 filter subject",
			mutate: func(cfg *ConsumerConfig) {
				cfg.FilterSubject = "events." + string([]byte{0xff})
			},
		},
		{
			name: "invalid UTF-8 quarantine subject",
			mutate: func(cfg *ConsumerConfig) {
				cfg.QuarantineSubject = "events." + string([]byte{0xff})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := testConsumerConfig()
			tt.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("Validate() error = nil, want subject isolation error")
			}
		})
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
