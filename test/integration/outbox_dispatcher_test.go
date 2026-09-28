package integration

import (
	"context"
	"os"
	"testing"
	"time"

	coreprop "github.com/Lamy210/go-template/internal/core/propagation"
	"github.com/Lamy210/go-template/internal/platform/messaging"
	"github.com/Lamy210/go-template/internal/platform/outbox"
	"github.com/nats-io/nats.go"
)

type outboxTraceKey struct{}

type outboxTestPropagator struct{}

func (outboxTestPropagator) Inject(ctx context.Context, carrier coreprop.TextMapCarrier) {
	if value, _ := ctx.Value(outboxTraceKey{}).(string); value != "" {
		carrier.Set("traceparent", value)
	}
}

func (outboxTestPropagator) Extract(
	ctx context.Context,
	carrier coreprop.TextMapCarrier,
) context.Context {
	value := carrier.Get("traceparent")
	if value == "" {
		return ctx
	}
	return context.WithValue(ctx, outboxTraceKey{}, value)
}

func TestOutboxDispatcherPublishesToJetStream(t *testing.T) {
	natsURL := os.Getenv("NATS_URL")
	if natsURL == "" {
		t.Skip("NATS_URL is not configured")
	}

	pool, ctx := openTestPool(t)
	if _, err := pool.Exec(ctx, "DELETE FROM outbox_events"); err != nil {
		t.Fatalf("clear outbox: %v", err)
	}

	propagator := outboxTestPropagator{}
	client, err := messaging.Open(
		messaging.ClientConfig{
			URL:            natsURL,
			Name:           "outbox-integration",
			ConnectTimeout: 2 * time.Second,
			ReconnectWait:  100 * time.Millisecond,
			MaxReconnects:  5,
			DrainTimeout:   3 * time.Second,
			RequestTimeout: time.Second,
		},
		messaging.WithPropagator(propagator),
	)
	if err != nil {
		t.Fatalf("open messaging client: %v", err)
	}
	t.Cleanup(func() {
		drainCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := client.Drain(drainCtx); err != nil {
			t.Errorf("drain messaging client: %v", err)
		}
	})

	stream := messaging.StreamConfig{
		Name:            "OUTBOX_EVENTS",
		Subjects:        []string{"outbox.events.>"},
		MaxConsumers:    4,
		MaxMessages:     100,
		MaxBytes:        1 << 20,
		MaxAge:          time.Hour,
		MaxMessageSize:  1 << 20,
		DuplicateWindow: time.Minute,
	}
	if err := client.EnsureStream(ctx, stream); err != nil {
		t.Fatalf("ensure outbox stream: %v", err)
	}

	rawConn, err := nats.Connect(natsURL, nats.Timeout(2*time.Second))
	if err != nil {
		t.Fatalf("open raw NATS subscriber: %v", err)
	}
	t.Cleanup(rawConn.Close)

	subscription, err := rawConn.SubscribeSync("outbox.events.created")
	if err != nil {
		t.Fatalf("subscribe to outbox subject: %v", err)
	}
	if err := rawConn.Flush(); err != nil {
		t.Fatalf("flush subscription: %v", err)
	}

	store, err := outbox.NewStore(pool)
	if err != nil {
		t.Fatalf("new outbox store: %v", err)
	}
	dispatcher, err := outbox.NewDispatcher(
		store,
		func(publishCtx context.Context, subject, eventID string, payload []byte) error {
			_, err := client.Publish(publishCtx, subject, eventID, payload)
			return err
		},
		propagator,
		outbox.DispatcherConfig{
			BatchSize:         10,
			PollInterval:      20 * time.Millisecond,
			Lease:             2 * time.Second,
			MaxAttempts:       3,
			RetryBaseDelay:    50 * time.Millisecond,
			RetryMaxDelay:     200 * time.Millisecond,
			PublishTimeout:    500 * time.Millisecond,
			StoreTimeout: 500 * time.Millisecond,
		},
	)
	if err != nil {
		t.Fatalf("new outbox dispatcher: %v", err)
	}

	const (
		eventID     = "outbox-e2e-event"
		traceparent = "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01"
	)
	enqueueCtx := context.WithValue(ctx, outboxTraceKey{}, traceparent)
	if err := outbox.Enqueue(
		enqueueCtx,
		pool,
		outbox.Event{
			ID:      eventID,
			Subject: "outbox.events.created",
			Payload: []byte("outbox-payload"),
		},
		propagator,
	); err != nil {
		t.Fatalf("enqueue outbox event: %v", err)
	}

	dispatchCtx, cancelDispatcher := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() {
		runErr <- dispatcher.Run(dispatchCtx)
	}()

	msg, err := subscription.NextMsg(5 * time.Second)
	if err != nil {
		cancelDispatcher()
		t.Fatalf("wait for dispatched message: %v", err)
	}
	if got := string(msg.Data); got != "outbox-payload" {
		t.Fatalf("message payload = %q", got)
	}
	if got := msg.Header.Get(nats.MsgIdHdr); got != eventID {
		t.Fatalf("Nats-Msg-Id = %q, want %q", got, eventID)
	}
	if got := msg.Header.Get("traceparent"); got != traceparent {
		t.Fatalf("traceparent = %q, want %q", got, traceparent)
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		var (
			published bool
			attempts  int
		)
		err := pool.QueryRow(
			ctx,
			`SELECT published_at IS NOT NULL, attempts
			 FROM outbox_events
			 WHERE event_id = $1`,
			eventID,
		).Scan(&published, &attempts)
		if err != nil {
			cancelDispatcher()
			t.Fatalf("read outbox settlement: %v", err)
		}
		if published {
			if attempts != 1 {
				cancelDispatcher()
				t.Fatalf("outbox attempts = %d, want 1", attempts)
			}
			break
		}
		if time.Now().After(deadline) {
			cancelDispatcher()
			t.Fatal("outbox event was not settled as published")
		}
		time.Sleep(20 * time.Millisecond)
	}

	cancelDispatcher()
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("dispatcher shutdown: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("dispatcher did not stop within %s", 2*time.Second)
	}
}
