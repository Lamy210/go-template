//go:build messaging

package integration

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Lamy210/go-template/internal/platform/messaging"
)

func TestJetStreamDedupRetryQuarantineAndDrain(t *testing.T) {
	natsURL := os.Getenv("NATS_URL")
	if natsURL == "" {
		t.Fatal("NATS_URL is required")
	}

	client, err := messaging.Open(messaging.ClientConfig{
		URL:            natsURL,
		Name:           "go-template-integration",
		ConnectTimeout: 2 * time.Second,
		ReconnectWait:  100 * time.Millisecond,
		MaxReconnects:  5,
		DrainTimeout:   3 * time.Second,
		RequestTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("open nats client: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := client.Drain(ctx); err != nil {
			t.Errorf("drain nats client: %v", err)
		}
	})

	stream := messaging.StreamConfig{
		Name:            "TEMPLATE_EVENTS",
		Subjects:        []string{"template.events.>"},
		MaxConsumers:    8,
		MaxMessages:     1_000,
		MaxBytes:        16 << 20,
		MaxAge:          time.Hour,
		MaxMessageSize:  1 << 20,
		DuplicateWindow: time.Minute,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := client.EnsureStream(ctx, stream); err != nil {
		t.Fatalf("ensure stream: %v", err)
	}

	first, err := client.Publish(ctx, "template.events.work", "dedup-1", []byte("deduplicated"))
	if err != nil {
		t.Fatalf("first publish: %v", err)
	}
	second, err := client.Publish(ctx, "template.events.work", "dedup-1", []byte("deduplicated"))
	if err != nil {
		t.Fatalf("second publish: %v", err)
	}
	if first.Duplicate {
		t.Fatal("first publish unexpectedly marked duplicate")
	}
	if !second.Duplicate {
		t.Fatal("second publish was not marked duplicate")
	}
	if first.Sequence != second.Sequence {
		t.Fatalf("deduplicated sequence changed: first=%d second=%d", first.Sequence, second.Sequence)
	}

	t.Run("retry then ack", func(t *testing.T) {
		var attempts atomic.Int32
		processed := make(chan struct{}, 1)
		consumerCtx, stop := context.WithCancel(ctx)
		errCh := make(chan error, 1)

		go func() {
			errCh <- client.RunConsumer(consumerCtx, workerConfig(stream, "retry-worker", "template.events.retry"), func(context.Context, messaging.Message) error {
				if attempts.Add(1) == 1 {
					return errors.New("retry requested by test")
				}
				processed <- struct{}{}
				return nil
			})
		}()

		if _, err := client.Publish(ctx, "template.events.retry", "retry-1", []byte("retry")); err != nil {
			t.Fatalf("publish retry message: %v", err)
		}

		select {
		case <-processed:
		case <-time.After(8 * time.Second):
			t.Fatal("timed out waiting for retry success")
		}
		if got := attempts.Load(); got != 2 {
			t.Fatalf("handler attempts = %d, want 2", got)
		}

		stop()
		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("consumer shutdown: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("consumer did not drain")
		}
	})

	t.Run("quarantine after bounded processing attempts", func(t *testing.T) {
		quarantined := make(chan messaging.Message, 1)

		quarantineCtx, stopQuarantine := context.WithCancel(ctx)
		quarantineErr := make(chan error, 1)
		go func() {
			cfg := workerConfig(stream, "quarantine-observer", "template.events.quarantine")
			cfg.QuarantineSubject = "template.events.quarantine.failed"
			quarantineErr <- client.RunConsumer(quarantineCtx, cfg, func(_ context.Context, msg messaging.Message) error {
				quarantined <- msg
				return nil
			})
		}()

		sourceCtx, stopSource := context.WithCancel(ctx)
		sourceErr := make(chan error, 1)
		go func() {
			cfg := workerConfig(stream, "failing-worker", "template.events.fail")
			cfg.ProcessAttempts = 1
			cfg.QuarantineAttempts = 2
			sourceErr <- client.RunConsumer(sourceCtx, cfg, func(context.Context, messaging.Message) error {
				return errors.New("processing failed by test")
			})
		}()

		if _, err := client.Publish(ctx, "template.events.fail", "fail-1", []byte("quarantine-me")); err != nil {
			t.Fatalf("publish failing message: %v", err)
		}

		select {
		case msg := <-quarantined:
			if string(msg.Data) != "quarantine-me" {
				t.Fatalf("quarantine payload = %q", msg.Data)
			}
		case <-time.After(8 * time.Second):
			t.Fatal("timed out waiting for quarantine message")
		}

		stopSource()
		stopQuarantine()
		for name, ch := range map[string]<-chan error{
			"source":     sourceErr,
			"quarantine": quarantineErr,
		} {
			select {
			case err := <-ch:
				if err != nil {
					t.Fatalf("%s consumer shutdown: %v", name, err)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("%s consumer did not drain", name)
			}
		}
	})

	if err := client.ReadinessCheck(time.Second)(ctx); err != nil {
		t.Fatalf("nats readiness: %v", err)
	}
}

func workerConfig(stream messaging.StreamConfig, durable, filter string) messaging.ConsumerConfig {
	return messaging.ConsumerConfig{
		Stream:             stream,
		Durable:            durable,
		FilterSubject:      filter,
		QuarantineSubject:  "template.events.quarantine",
		AckWait:            2 * time.Second,
		ProcessAttempts:    2,
		QuarantineAttempts: 2,
		MaxAckPending:      8,
		RetryDelay:         100 * time.Millisecond,
		HandlerTimeout:     time.Second,
		AckTimeout:         time.Second,
		PullExpiry:         time.Second,
	}
}
