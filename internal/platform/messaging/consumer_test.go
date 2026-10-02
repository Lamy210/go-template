package messaging

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

func TestInvokeHandlerReturnsHandlerError(t *testing.T) {
	t.Parallel()

	want := errors.New("handler failed")
	got := invokeHandler(
		context.Background(),
		func(context.Context, Message) error {
			return want
		},
		Message{},
	)
	if !errors.Is(got, want) {
		t.Fatalf("invokeHandler() error = %v, want wrapped/identical %v", got, want)
	}
}

func TestInvokeHandlerContainsPanicWithoutExposingValue(t *testing.T) {
	t.Parallel()

	const sensitive = "secret-bearing panic value"
	got := invokeHandler(
		context.Background(),
		func(context.Context, Message) error {
			panic(sensitive)
		},
		Message{},
	)
	if got == nil {
		t.Fatal("invokeHandler() error = nil, want panic conversion")
	}
	if _, ok := got.(handlerPanicError); !ok {
		t.Fatalf("invokeHandler() error type = %T, want handlerPanicError", got)
	}
	if strings.Contains(got.Error(), sensitive) {
		t.Fatalf("panic value leaked through error: %q", got.Error())
	}
}

func TestInvokeHandlerTreatsExpiredContextAsFailure(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	got := invokeHandler(
		ctx,
		func(ctx context.Context, _ Message) error {
			<-ctx.Done()
			return nil
		},
		Message{},
	)
	if !errors.Is(got, context.DeadlineExceeded) {
		t.Fatalf("invokeHandler() error = %v, want deadline exceeded", got)
	}
}

func TestValidateConsumerSettlementBudgetIncludesPublishTimeout(t *testing.T) {
	t.Parallel()

	cfg := testConsumerConfig()
	cfg.AckWait = 4 * time.Second
	cfg.HandlerTimeout = time.Second
	cfg.AckTimeout = time.Second

	if err := validateConsumerSettlementBudget(cfg, 2*time.Second); err == nil {
		t.Fatal("exact handler+publish+ack budget must not be accepted")
	}

	cfg.AckWait += time.Nanosecond
	if err := validateConsumerSettlementBudget(cfg, 2*time.Second); err != nil {
		t.Fatalf("settlement budget with strict slack error = %v", err)
	}
}

func TestDeliveryAttemptLimitsCountFinalProcessingDeliveryAsFirstQuarantineAttempt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		processAttempts    int
		quarantineAttempts int
		wantProcess        uint64
		wantTotal          uint64
	}{
		{
			name:               "single quarantine attempt needs no extra delivery",
			processAttempts:    1,
			quarantineAttempts: 1,
			wantProcess:        1,
			wantTotal:          1,
		},
		{
			name:               "two quarantine attempts need one extra delivery",
			processAttempts:    3,
			quarantineAttempts: 2,
			wantProcess:        3,
			wantTotal:          4,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			process, total, err := deliveryAttemptLimits(ConsumerConfig{
				ProcessAttempts:    tt.processAttempts,
				QuarantineAttempts: tt.quarantineAttempts,
			})
			if err != nil {
				t.Fatalf("deliveryAttemptLimits() error = %v", err)
			}
			if process != tt.wantProcess {
				t.Fatalf("process attempts = %d, want %d", process, tt.wantProcess)
			}
			if total != tt.wantTotal {
				t.Fatalf("total deliveries = %d, want %d", total, tt.wantTotal)
			}
		})
	}
}

func TestDeliveryAttemptLimitsRejectsNonPositiveAttempts(t *testing.T) {
	t.Parallel()

	for _, cfg := range []ConsumerConfig{
		{ProcessAttempts: 0, QuarantineAttempts: 1},
		{ProcessAttempts: 1, QuarantineAttempts: 0},
	} {
		if _, _, err := deliveryAttemptLimits(cfg); err == nil {
			t.Fatalf("deliveryAttemptLimits(%+v) error = nil", cfg)
		}
	}
}

func TestConsumerConfigAttemptOverflowMatchesEffectiveMaxDeliver(t *testing.T) {
	t.Parallel()

	cfg := testConsumerConfig()
	cfg.ProcessAttempts = math.MaxInt
	cfg.QuarantineAttempts = 1
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() with MaxInt process and one quarantine attempt = %v", err)
	}

	cfg.QuarantineAttempts = 2
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() overflow error = nil")
	}
}

func TestManagedConsumerConfigUsesExactAttemptBudget(t *testing.T) {
	t.Parallel()

	cfg := testConsumerConfig()
	cfg.ProcessAttempts = 3
	cfg.QuarantineAttempts = 2

	got := managedConsumerConfig(cfg)
	if got.MaxDeliver != 4 {
		t.Fatalf("MaxDeliver = %d, want 4", got.MaxDeliver)
	}
}

func TestManagedConsumerConfigMatchesAllowsUnmanagedMetadata(t *testing.T) {
	t.Parallel()

	cfg := testConsumerConfig()
	actual := managedConsumerConfig(cfg)
	actual.Description = "operator-owned description"
	actual.Replicas = 3
	actual.SampleFrequency = "10"

	if !managedConsumerConfigMatches(actual, cfg) {
		t.Fatal("managedConsumerConfigMatches() rejected unmanaged consumer metadata")
	}
}

func TestManagedConsumerConfigMatchesRejectsProcessingDrift(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*jetstream.ConsumerConfig)
	}{
		{
			name: "ack wait",
			mutate: func(actual *jetstream.ConsumerConfig) {
				actual.AckWait += time.Second
			},
		},
		{
			name: "max deliver",
			mutate: func(actual *jetstream.ConsumerConfig) {
				actual.MaxDeliver++
			},
		},
		{
			name: "max ack pending",
			mutate: func(actual *jetstream.ConsumerConfig) {
				actual.MaxAckPending++
			},
		},
		{
			name: "filter subject",
			mutate: func(actual *jetstream.ConsumerConfig) {
				actual.FilterSubject = "events.other"
			},
		},
		{
			name: "server backoff",
			mutate: func(actual *jetstream.ConsumerConfig) {
				actual.BackOff = []time.Duration{time.Second}
			},
		},
		{
			name: "push consumer",
			mutate: func(actual *jetstream.ConsumerConfig) {
				actual.DeliverSubject = "_INBOX.push"
			},
		},
		{
			name: "headers only",
			mutate: func(actual *jetstream.ConsumerConfig) {
				actual.HeadersOnly = true
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := testConsumerConfig()
			actual := managedConsumerConfig(cfg)
			tt.mutate(&actual)

			if managedConsumerConfigMatches(actual, cfg) {
				t.Fatal("managedConsumerConfigMatches() accepted processing drift")
			}
		})
	}
}

func testConsumerConfig() ConsumerConfig {
	return ConsumerConfig{
		Stream: StreamConfig{
			Name:            "TEST",
			Subjects:        []string{"events.>"},
			MaxConsumers:    8,
			MaxMessages:     100,
			MaxBytes:        1 << 20,
			MaxAge:          time.Hour,
			MaxMessageSize:  1 << 20,
			DuplicateWindow: time.Minute,
		},
		Durable:            "worker",
		FilterSubject:      "events.work",
		QuarantineSubject:  "events.quarantine",
		AckWait:            3 * time.Second,
		ProcessAttempts:    2,
		QuarantineAttempts: 2,
		MaxAckPending:      8,
		RetryDelay:         100 * time.Millisecond,
		HandlerTimeout:     time.Second,
		AckTimeout:         time.Second,
		PullExpiry:         time.Second,
	}
}
