//go:build messaging

package messaging_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	coreprop "github.com/Lamy210/go-template/internal/core/propagation"
	"github.com/Lamy210/go-template/internal/platform/messaging"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const (
	testCorrelationHeader = "X-Test-Correlation"
	testTraceHeader       = "X-Test-Trace-Operation"
)

type testPropagationKey struct{}
type testTraceOperationKey struct{}

type testPropagator struct{}

func (testPropagator) Inject(ctx context.Context, carrier coreprop.TextMapCarrier) {
	if value, _ := ctx.Value(testPropagationKey{}).(string); value != "" {
		carrier.Set(testCorrelationHeader, value)
	}
	if value, _ := ctx.Value(testTraceOperationKey{}).(string); value != "" {
		carrier.Set(testTraceHeader, value)
	}
}

func (testPropagator) Extract(ctx context.Context, carrier coreprop.TextMapCarrier) context.Context {
	if value := carrier.Get(testCorrelationHeader); value != "" {
		ctx = context.WithValue(ctx, testPropagationKey{}, value)
	}
	if value := carrier.Get(testTraceHeader); value != "" {
		ctx = context.WithValue(ctx, testTraceOperationKey{}, value)
	}
	return ctx
}

type testTracer struct {
	publishStarted atomic.Int32
	publishEnded   atomic.Int32
	processStarted atomic.Int32
	processEnded   atomic.Int32
}

func (t *testTracer) StartPublish(
	ctx context.Context,
	destination string,
) (context.Context, func(error)) {
	t.publishStarted.Add(1)
	ctx = context.WithValue(
		ctx,
		testTraceOperationKey{},
		"publish:"+destination,
	)
	return ctx, func(error) {
		t.publishEnded.Add(1)
	}
}

func (t *testTracer) StartProcess(
	ctx context.Context,
	destination string,
) (context.Context, func(error)) {
	t.processStarted.Add(1)
	parent, _ := ctx.Value(testTraceOperationKey{}).(string)
	ctx = context.WithValue(
		ctx,
		testTraceOperationKey{},
		"process:"+destination+" parent="+parent,
	)
	return ctx, func(error) {
		t.processEnded.Add(1)
	}
}

type natsControlHeaderIntegrationPropagator struct{}

func (natsControlHeaderIntegrationPropagator) Inject(
	_ context.Context,
	carrier coreprop.TextMapCarrier,
) {
	carrier.Set(testCorrelationHeader, "must-be-dropped")
	carrier.Set(jetstream.ExpectedStreamHeader, "WRONG_STREAM")
}

func (natsControlHeaderIntegrationPropagator) Extract(
	ctx context.Context,
	_ coreprop.TextMapCarrier,
) context.Context {
	return ctx
}

type collidingHeaderIntegrationPropagator struct{}

func (collidingHeaderIntegrationPropagator) Inject(
	_ context.Context,
	carrier coreprop.TextMapCarrier,
) {
	carrier.Set(testCorrelationHeader, "must-be-dropped")
	carrier.Set(jetstream.MsgIDHeader, "observability-overwrite")
}

func (collidingHeaderIntegrationPropagator) Extract(
	ctx context.Context,
	_ coreprop.TextMapCarrier,
) context.Context {
	return ctx
}

type unstableHeaderValueIntegrationPropagator struct{}

func (unstableHeaderValueIntegrationPropagator) Inject(
	_ context.Context,
	carrier coreprop.TextMapCarrier,
) {
	carrier.Set(testCorrelationHeader, "must-be-dropped")
	carrier.Set("X-Test-Unstable", "line1\nline2")
}

func (unstableHeaderValueIntegrationPropagator) Extract(
	ctx context.Context,
	_ coreprop.TextMapCarrier,
) context.Context {
	return ctx
}

type invalidHeaderIntegrationPropagator struct{}

func (invalidHeaderIntegrationPropagator) Inject(
	_ context.Context,
	carrier coreprop.TextMapCarrier,
) {
	carrier.Set(testCorrelationHeader, "must-be-dropped")
	carrier.Set("invalid/header", "bad")
}

func (invalidHeaderIntegrationPropagator) Extract(
	ctx context.Context,
	_ coreprop.TextMapCarrier,
) context.Context {
	return ctx
}

type panickingIntegrationPropagator struct{}

func (panickingIntegrationPropagator) Inject(
	context.Context,
	coreprop.TextMapCarrier,
) {
	panic("sensitive propagation inject panic")
}

func (panickingIntegrationPropagator) Extract(
	context.Context,
	coreprop.TextMapCarrier,
) context.Context {
	panic("sensitive propagation extract panic")
}

type panickingIntegrationTracer struct{}

func (panickingIntegrationTracer) StartPublish(
	context.Context,
	string,
) (context.Context, func(error)) {
	panic("sensitive publish tracer panic")
}

func (panickingIntegrationTracer) StartProcess(
	context.Context,
	string,
) (context.Context, func(error)) {
	panic("sensitive process tracer panic")
}

func TestOpenContainsOptionPanic(t *testing.T) {
	natsURL := os.Getenv("NATS_URL")
	if natsURL == "" {
		t.Fatal("NATS_URL is required")
	}

	const sensitive = "sensitive startup option panic"
	client, err := messaging.Open(messaging.ClientConfig{
		URL:            natsURL,
		Name:           "go-template-option-panic",
		ConnectTimeout: 2 * time.Second,
		ReconnectWait:  100 * time.Millisecond,
		MaxReconnects:  5,
		DrainTimeout:   3 * time.Second,
		RequestTimeout: 2 * time.Second,
	}, messaging.Option(func(*messaging.Client) {
		panic(sensitive)
	}))
	if client != nil {
		client.Close()
		t.Fatal("Open() client != nil after option panic")
	}
	if err == nil {
		t.Fatal("Open() error = nil after option panic")
	}
	if got := err.Error(); got != "configure nats client" {
		t.Fatalf("Open() error text = %q, want sanitized operation", got)
	}
	if strings.Contains(err.Error(), sensitive) {
		t.Fatalf("Open() exposed panic value: %q", err.Error())
	}
}

func TestJetStreamDedupRetryQuarantineAndDrain(t *testing.T) {
	natsURL := os.Getenv("NATS_URL")
	if natsURL == "" {
		t.Fatal("NATS_URL is required")
	}

	tracer := &testTracer{}
	client, err := messaging.Open(messaging.ClientConfig{
		URL:            natsURL,
		Name:           "go-template-integration",
		ConnectTimeout: 2 * time.Second,
		ReconnectWait:  100 * time.Millisecond,
		MaxReconnects:  5,
		DrainTimeout:   3 * time.Second,
		RequestTimeout: 2 * time.Second,
	},
		messaging.WithPropagator(testPropagator{}),
		messaging.WithTracer(tracer),
	)
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
		MaxConsumers:    16,
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

	t.Run("preflights managed stream message size including headers", func(t *testing.T) {
		smallStream := messaging.StreamConfig{
			Name:            "TEMPLATE_SMALL_MESSAGES",
			Subjects:        []string{"template.small.>"},
			MaxConsumers:    2,
			MaxMessages:     100,
			MaxBytes:        1 << 20,
			MaxAge:          time.Hour,
			MaxMessageSize:  256,
			DuplicateWindow: time.Minute,
		}
		if err := client.EnsureStream(ctx, smallStream); err != nil {
			t.Fatalf("ensure small-message stream: %v", err)
		}

		_, err := client.Publish(
			ctx,
			"template.small.work",
			"size-preflight-message-id",
			make([]byte, 220),
		)
		if !errors.Is(err, messaging.ErrMessageTooLarge) {
			t.Fatalf("oversized publish error = %v, want messaging.ErrMessageTooLarge", err)
		}

		if _, err := client.Publish(
			ctx,
			"template.small.work",
			"size-preflight-small",
			[]byte("ok"),
		); err != nil {
			t.Fatalf("small publish after preflight rejection: %v", err)
		}

		const fallbackSubject = "template.small.propagation-fallback"
		rawConn, err := nats.Connect(natsURL)
		if err != nil {
			t.Fatalf("open raw subscriber connection: %v", err)
		}
		t.Cleanup(rawConn.Close)

		sub, err := rawConn.SubscribeSync(fallbackSubject)
		if err != nil {
			t.Fatalf("subscribe propagation-fallback subject: %v", err)
		}
		if err := rawConn.Flush(); err != nil {
			t.Fatalf("flush propagation-fallback subscription: %v", err)
		}

		const fallbackMsgID = "size-propagation-fallback"
		publishCtx := context.WithValue(
			ctx,
			testPropagationKey{},
			strings.Repeat("c", 64),
		)
		if _, err := client.Publish(
			publishCtx,
			fallbackSubject,
			fallbackMsgID,
			make([]byte, 170),
		); err != nil {
			t.Fatalf("publish after dropping oversized propagation: %v", err)
		}

		received, err := sub.NextMsg(5 * time.Second)
		if err != nil {
			t.Fatalf("receive propagation-fallback message: %v", err)
		}
		if got := received.Header.Get(testCorrelationHeader); got != "" {
			t.Fatalf("correlation propagation header = %q, want dropped", got)
		}
		if got := received.Header.Get(testTraceHeader); got != "" {
			t.Fatalf("trace propagation header = %q, want dropped", got)
		}
		if got := received.Header.Get(jetstream.MsgIDHeader); got != fallbackMsgID {
			t.Fatalf("message ID header = %q, want %q", got, fallbackMsgID)
		}
		if got := len(received.Data); got != 170 {
			t.Fatalf("payload len = %d, want 170", got)
		}
	})

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

	t.Run("propagates context through message headers", func(t *testing.T) {
		const correlation = "publish-to-handler"
		observed := make(chan string, 1)
		consumerCtx, stop := context.WithCancel(ctx)
		errCh := make(chan error, 1)

		go func() {
			errCh <- client.RunConsumer(
				consumerCtx,
				workerConfig(stream, "propagation-worker", "template.events.propagation"),
				func(handlerCtx context.Context, _ messaging.Message) error {
					value, _ := handlerCtx.Value(testPropagationKey{}).(string)
					observed <- value
					return nil
				},
			)
		}()

		publishCtx := context.WithValue(ctx, testPropagationKey{}, correlation)
		if _, err := client.Publish(
			publishCtx,
			"template.events.propagation",
			"propagation-1",
			[]byte("propagation"),
		); err != nil {
			t.Fatalf("publish propagation message: %v", err)
		}

		select {
		case got := <-observed:
			if got != correlation {
				t.Fatalf("handler correlation = %q, want %q", got, correlation)
			}
		case <-time.After(8 * time.Second):
			t.Fatal("timed out waiting for propagated context")
		}

		stop()
		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("propagation consumer shutdown: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("propagation consumer did not drain")
		}
	})

	t.Run("traces publish and handler with propagated operation context", func(t *testing.T) {
		const subject = "template.events.telemetry"
		observed := make(chan string, 1)
		consumerCtx, stop := context.WithCancel(ctx)
		errCh := make(chan error, 1)

		go func() {
			errCh <- client.RunConsumer(
				consumerCtx,
				workerConfig(stream, "telemetry-worker", subject),
				func(handlerCtx context.Context, _ messaging.Message) error {
					value, _ := handlerCtx.Value(testTraceOperationKey{}).(string)
					observed <- value
					return nil
				},
			)
		}()

		if _, err := client.Publish(
			ctx,
			subject,
			"telemetry-1",
			[]byte("telemetry"),
		); err != nil {
			t.Fatalf("publish telemetry message: %v", err)
		}

		select {
		case got := <-observed:
			want := "process:" + subject + " parent=publish:" + subject
			if got != want {
				t.Fatalf("handler trace operation = %q, want %q", got, want)
			}
		case <-time.After(8 * time.Second):
			t.Fatal("timed out waiting for traced handler")
		}

		stop()
		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("telemetry consumer shutdown: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("telemetry consumer did not drain")
		}

		if tracer.publishStarted.Load() == 0 || tracer.publishEnded.Load() == 0 {
			t.Fatal("publish tracer lifecycle was not invoked")
		}
		if tracer.processStarted.Load() == 0 || tracer.processEnded.Load() == 0 {
			t.Fatal("process tracer lifecycle was not invoked")
		}
	})

	t.Run("propagation cannot inject NATS publish controls", func(t *testing.T) {
		const subject = "template.events.propagation-nats-control"
		const msgID = "propagation-nats-control-1"

		hookClient, err := messaging.Open(
			messaging.ClientConfig{
				URL:            natsURL,
				Name:           "go-template-propagation-nats-control",
				ConnectTimeout: 2 * time.Second,
				ReconnectWait:  100 * time.Millisecond,
				MaxReconnects:  5,
				DrainTimeout:   3 * time.Second,
				RequestTimeout: 2 * time.Second,
			},
			messaging.WithPropagator(natsControlHeaderIntegrationPropagator{}),
		)
		if err != nil {
			t.Fatalf("open NATS-control nats client: %v", err)
		}
		t.Cleanup(func() {
			drainCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := hookClient.Drain(drainCtx); err != nil {
				t.Errorf("drain NATS-control nats client: %v", err)
			}
		})

		rawConn, err := nats.Connect(natsURL)
		if err != nil {
			t.Fatalf("open NATS-control subscriber: %v", err)
		}
		t.Cleanup(rawConn.Close)
		sub, err := rawConn.SubscribeSync(subject)
		if err != nil {
			t.Fatalf("subscribe NATS-control subject: %v", err)
		}
		if err := rawConn.Flush(); err != nil {
			t.Fatalf("flush NATS-control subscription: %v", err)
		}

		ack, err := hookClient.Publish(ctx, subject, msgID, []byte("business"))
		if err != nil {
			t.Fatalf("publish with staged NATS control header: %v", err)
		}
		if ack.Duplicate {
			t.Fatal("first NATS-control publish unexpectedly marked duplicate")
		}

		received, err := sub.NextMsg(5 * time.Second)
		if err != nil {
			t.Fatalf("receive NATS-control message: %v", err)
		}
		if got := received.Header.Get(jetstream.ExpectedStreamHeader); got != "" {
			t.Fatalf("expected-stream control header = %q, want dropped", got)
		}
		if got := received.Header.Get(testCorrelationHeader); got != "" {
			t.Fatalf("staged propagation header = %q, want dropped", got)
		}
		if got := received.Header.Get(jetstream.MsgIDHeader); got != msgID {
			t.Fatalf("message ID = %q, want %q", got, msgID)
		}
		if got := string(received.Data); got != "business" {
			t.Fatalf("payload = %q, want business", got)
		}
	})

	t.Run("propagation cannot overwrite message identity", func(t *testing.T) {
		const subject = "template.events.propagation-header-collision"

		hookClient, err := messaging.Open(
			messaging.ClientConfig{
				URL:            natsURL,
				Name:           "go-template-propagation-header-collision",
				ConnectTimeout: 2 * time.Second,
				ReconnectWait:  100 * time.Millisecond,
				MaxReconnects:  5,
				DrainTimeout:   3 * time.Second,
				RequestTimeout: 2 * time.Second,
			},
			messaging.WithPropagator(collidingHeaderIntegrationPropagator{}),
		)
		if err != nil {
			t.Fatalf("open header-collision nats client: %v", err)
		}
		t.Cleanup(func() {
			drainCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := hookClient.Drain(drainCtx); err != nil {
				t.Errorf("drain header-collision nats client: %v", err)
			}
		})

		rawConn, err := nats.Connect(natsURL)
		if err != nil {
			t.Fatalf("open header-collision subscriber: %v", err)
		}
		t.Cleanup(rawConn.Close)
		sub, err := rawConn.SubscribeSync(subject)
		if err != nil {
			t.Fatalf("subscribe header-collision subject: %v", err)
		}
		if err := rawConn.Flush(); err != nil {
			t.Fatalf("flush header-collision subscription: %v", err)
		}

		first, err := hookClient.Publish(ctx, subject, "business-id-1", []byte("first"))
		if err != nil {
			t.Fatalf("first header-collision publish: %v", err)
		}
		second, err := hookClient.Publish(ctx, subject, "business-id-2", []byte("second"))
		if err != nil {
			t.Fatalf("second header-collision publish: %v", err)
		}
		if first.Duplicate || second.Duplicate {
			t.Fatalf(
				"distinct business IDs were deduplicated: first=%t second=%t",
				first.Duplicate,
				second.Duplicate,
			)
		}
		if first.Sequence == second.Sequence {
			t.Fatalf("distinct business IDs share sequence %d", first.Sequence)
		}

		for _, want := range []struct {
			id      string
			payload string
		}{
			{id: "business-id-1", payload: "first"},
			{id: "business-id-2", payload: "second"},
		} {
			received, err := sub.NextMsg(5 * time.Second)
			if err != nil {
				t.Fatalf("receive header-collision message: %v", err)
			}
			if got := received.Header.Get(jetstream.MsgIDHeader); got != want.id {
				t.Fatalf("message ID = %q, want %q", got, want.id)
			}
			if got := received.Header.Get(testCorrelationHeader); got != "" {
				t.Fatalf("staged propagation header = %q, want dropped", got)
			}
			if got := string(received.Data); got != want.payload {
				t.Fatalf("payload = %q, want %q", got, want.payload)
			}
		}
	})

	t.Run("mutable propagation value does not alter business publish", func(t *testing.T) {
		const subject = "template.events.mutable-propagation-value"
		const msgID = "mutable-propagation-value-1"

		hookClient, err := messaging.Open(
			messaging.ClientConfig{
				URL:            natsURL,
				Name:           "go-template-mutable-propagation-value",
				ConnectTimeout: 2 * time.Second,
				ReconnectWait:  100 * time.Millisecond,
				MaxReconnects:  5,
				DrainTimeout:   3 * time.Second,
				RequestTimeout: 2 * time.Second,
			},
			messaging.WithPropagator(unstableHeaderValueIntegrationPropagator{}),
		)
		if err != nil {
			t.Fatalf("open mutable-value nats client: %v", err)
		}
		t.Cleanup(func() {
			drainCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := hookClient.Drain(drainCtx); err != nil {
				t.Errorf("drain mutable-value nats client: %v", err)
			}
		})

		rawConn, err := nats.Connect(natsURL)
		if err != nil {
			t.Fatalf("open mutable-value subscriber: %v", err)
		}
		t.Cleanup(rawConn.Close)
		sub, err := rawConn.SubscribeSync(subject)
		if err != nil {
			t.Fatalf("subscribe mutable-value subject: %v", err)
		}
		if err := rawConn.Flush(); err != nil {
			t.Fatalf("flush mutable-value subscription: %v", err)
		}

		if _, err := hookClient.Publish(ctx, subject, msgID, []byte("business")); err != nil {
			t.Fatalf("publish with mutable propagation value: %v", err)
		}

		received, err := sub.NextMsg(5 * time.Second)
		if err != nil {
			t.Fatalf("receive mutable-value message: %v", err)
		}
		if got := received.Header.Get(testCorrelationHeader); got != "" {
			t.Fatalf("stable staged propagation header = %q, want dropped", got)
		}
		if got := received.Header.Get("X-Test-Unstable"); got != "" {
			t.Fatalf("mutable staged propagation header = %q, want dropped", got)
		}
		if got := received.Header.Get(jetstream.MsgIDHeader); got != msgID {
			t.Fatalf("message ID = %q, want %q", got, msgID)
		}
		if got := string(received.Data); got != "business" {
			t.Fatalf("payload = %q, want business", got)
		}
	})

	t.Run("invalid propagation header key does not stop publish", func(t *testing.T) {
		const subject = "template.events.invalid-propagation-header"
		const msgID = "invalid-propagation-header-1"

		hookClient, err := messaging.Open(
			messaging.ClientConfig{
				URL:            natsURL,
				Name:           "go-template-invalid-propagation-header",
				ConnectTimeout: 2 * time.Second,
				ReconnectWait:  100 * time.Millisecond,
				MaxReconnects:  5,
				DrainTimeout:   3 * time.Second,
				RequestTimeout: 2 * time.Second,
			},
			messaging.WithPropagator(invalidHeaderIntegrationPropagator{}),
		)
		if err != nil {
			t.Fatalf("open invalid-header nats client: %v", err)
		}
		t.Cleanup(func() {
			drainCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := hookClient.Drain(drainCtx); err != nil {
				t.Errorf("drain invalid-header nats client: %v", err)
			}
		})

		rawConn, err := nats.Connect(natsURL)
		if err != nil {
			t.Fatalf("open invalid-header subscriber: %v", err)
		}
		t.Cleanup(rawConn.Close)
		sub, err := rawConn.SubscribeSync(subject)
		if err != nil {
			t.Fatalf("subscribe invalid-header subject: %v", err)
		}
		if err := rawConn.Flush(); err != nil {
			t.Fatalf("flush invalid-header subscription: %v", err)
		}

		if _, err := hookClient.Publish(ctx, subject, msgID, []byte("business")); err != nil {
			t.Fatalf("publish with invalid propagation header: %v", err)
		}

		received, err := sub.NextMsg(5 * time.Second)
		if err != nil {
			t.Fatalf("receive invalid-header message: %v", err)
		}
		if got := received.Header.Get(testCorrelationHeader); got != "" {
			t.Fatalf("staged propagation header = %q, want dropped", got)
		}
		if _, ok := received.Header["invalid/header"]; ok {
			t.Fatal("invalid propagation header reached broker")
		}
		if got := received.Header.Get(jetstream.MsgIDHeader); got != msgID {
			t.Fatalf("message ID = %q, want %q", got, msgID)
		}
		if got := string(received.Data); got != "business" {
			t.Fatalf("payload = %q, want business", got)
		}
	})

	t.Run("observability hook panics do not stop publish or consume", func(t *testing.T) {
		const subject = "template.events.observability-hook-panic"

		hookClient, err := messaging.Open(
			messaging.ClientConfig{
				URL:            natsURL,
				Name:           "go-template-observability-hook-panic",
				ConnectTimeout: 2 * time.Second,
				ReconnectWait:  100 * time.Millisecond,
				MaxReconnects:  5,
				DrainTimeout:   3 * time.Second,
				RequestTimeout: 2 * time.Second,
			},
			messaging.WithPropagator(panickingIntegrationPropagator{}),
			messaging.WithTracer(panickingIntegrationTracer{}),
		)
		if err != nil {
			t.Fatalf("open hook-panic nats client: %v", err)
		}
		t.Cleanup(func() {
			drainCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := hookClient.Drain(drainCtx); err != nil {
				t.Errorf("drain hook-panic nats client: %v", err)
			}
		})

		processed := make(chan struct{}, 1)
		consumerCtx, stop := context.WithCancel(ctx)
		errCh := make(chan error, 1)
		go func() {
			errCh <- hookClient.RunConsumer(
				consumerCtx,
				workerConfig(stream, "observability-hook-panic-worker", subject),
				func(context.Context, messaging.Message) error {
					processed <- struct{}{}
					return nil
				},
			)
		}()

		if _, err := hookClient.Publish(
			ctx,
			subject,
			"observability-hook-panic-1",
			[]byte("observable"),
		); err != nil {
			t.Fatalf("publish with panicking observability hooks: %v", err)
		}

		select {
		case <-processed:
		case <-time.After(8 * time.Second):
			t.Fatal("handler did not run after observability hook panics")
		}

		stop()
		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("hook-panic consumer shutdown: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("hook-panic consumer did not drain")
		}
	})

	t.Run("handler panic retries without crashing consumer", func(t *testing.T) {
		const subject = "template.events.panic"
		var attempts atomic.Int32
		processed := make(chan struct{}, 1)
		consumerCtx, stop := context.WithCancel(ctx)
		errCh := make(chan error, 1)

		go func() {
			errCh <- client.RunConsumer(
				consumerCtx,
				workerConfig(stream, "panic-worker", subject),
				func(context.Context, messaging.Message) error {
					if attempts.Add(1) == 1 {
						panic("sensitive panic value")
					}
					processed <- struct{}{}
					return nil
				},
			)
		}()

		if _, err := client.Publish(
			ctx,
			subject,
			"panic-1",
			[]byte("panic-retry"),
		); err != nil {
			t.Fatalf("publish panic message: %v", err)
		}

		select {
		case <-processed:
		case <-time.After(8 * time.Second):
			t.Fatal("timed out waiting for post-panic retry success")
		}
		if got := attempts.Load(); got != 2 {
			t.Fatalf("handler attempts = %d, want 2", got)
		}

		stop()
		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("panic consumer shutdown: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("panic consumer did not drain")
		}
	})

	t.Run("serial consumer does not prefetch past active handler", func(t *testing.T) {
		const subject = "template.events.serial-prefetch"

		serialClient, err := messaging.Open(messaging.ClientConfig{
			URL:            natsURL,
			Name:           "go-template-serial-prefetch",
			ConnectTimeout: 2 * time.Second,
			ReconnectWait:  100 * time.Millisecond,
			MaxReconnects:  5,
			DrainTimeout:   3 * time.Second,
			RequestTimeout: 500 * time.Millisecond,
		})
		if err != nil {
			t.Fatalf("open serial-prefetch client: %v", err)
		}
		t.Cleanup(func() {
			drainCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := serialClient.Drain(drainCtx); err != nil {
				t.Errorf("drain serial-prefetch client: %v", err)
			}
		})

		type observedDelivery struct {
			payload      string
			numDelivered uint64
		}
		deliveries := make(chan observedDelivery, 32)
		consumerCtx, stop := context.WithCancel(ctx)
		errCh := make(chan error, 1)
		go func() {
			cfg := workerConfig(stream, "serial-prefetch-worker", subject)
			cfg.AckWait = 900 * time.Millisecond
			cfg.MaxAckPending = 8
			cfg.HandlerTimeout = 200 * time.Millisecond
			cfg.AckTimeout = 100 * time.Millisecond
			errCh <- serialClient.RunConsumer(
				consumerCtx,
				cfg,
				func(handlerCtx context.Context, msg messaging.Message) error {
					timer := time.NewTimer(150 * time.Millisecond)
					defer timer.Stop()
					select {
					case <-timer.C:
					case <-handlerCtx.Done():
						return handlerCtx.Err()
					}
					deliveries <- observedDelivery{
						payload:      string(msg.Data),
						numDelivered: msg.NumDelivered,
					}
					return nil
				},
			)
		}()

		messageIDs := []string{
			"serial-prefetch-1",
			"serial-prefetch-2",
			"serial-prefetch-3",
			"serial-prefetch-4",
			"serial-prefetch-5",
			"serial-prefetch-6",
			"serial-prefetch-7",
			"serial-prefetch-8",
		}
		for _, messageID := range messageIDs {
			if _, err := serialClient.Publish(ctx, subject, messageID, []byte(messageID)); err != nil {
				stop()
				t.Fatalf("publish %s: %v", messageID, err)
			}
		}

		seen := make(map[string]struct{}, len(messageIDs))
		deadline := time.NewTimer(5 * time.Second)
		defer deadline.Stop()
		for len(seen) < len(messageIDs) {
			select {
			case delivery := <-deliveries:
				if delivery.numDelivered != 1 {
					stop()
					t.Fatalf(
						"delivery %q NumDelivered = %d, want first delivery",
						delivery.payload,
						delivery.numDelivered,
					)
				}
				seen[delivery.payload] = struct{}{}
			case err := <-errCh:
				stop()
				t.Fatalf("serial-prefetch consumer stopped early: %v", err)
			case <-deadline.C:
				stop()
				t.Fatalf("timed out after %d/%d unique deliveries", len(seen), len(messageIDs))
			}
		}

		select {
		case delivery := <-deliveries:
			stop()
			t.Fatalf(
				"unexpected redelivery after unique set completed: payload=%q delivered=%d",
				delivery.payload,
				delivery.numDelivered,
			)
		case err := <-errCh:
			stop()
			t.Fatalf("serial-prefetch consumer stopped early: %v", err)
		case <-time.After(time.Second):
		}

		stop()
		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("serial-prefetch consumer shutdown: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("serial-prefetch consumer did not drain")
		}
	})

	t.Run("handler deadline nil return retries", func(t *testing.T) {
		const subject = "template.events.deadline"
		var attempts atomic.Int32
		processed := make(chan struct{}, 1)
		consumerCtx, stop := context.WithCancel(ctx)
		errCh := make(chan error, 1)

		go func() {
			cfg := workerConfig(stream, "deadline-worker", subject)
			cfg.HandlerTimeout = 100 * time.Millisecond
			cfg.AckTimeout = 100 * time.Millisecond
			cfg.AckWait = 3 * time.Second
			cfg.RetryDelay = 50 * time.Millisecond
			errCh <- client.RunConsumer(
				consumerCtx,
				cfg,
				func(handlerCtx context.Context, _ messaging.Message) error {
					if attempts.Add(1) == 1 {
						<-handlerCtx.Done()
						return nil
					}
					processed <- struct{}{}
					return nil
				},
			)
		}()

		if _, err := client.Publish(
			ctx,
			subject,
			"deadline-1",
			[]byte("deadline-retry"),
		); err != nil {
			t.Fatalf("publish deadline message: %v", err)
		}

		select {
		case <-processed:
		case <-time.After(8 * time.Second):
			t.Fatal("timed out waiting for post-deadline retry success")
		}
		if got := attempts.Load(); got != 2 {
			t.Fatalf("handler attempts = %d, want 2", got)
		}

		stop()
		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("deadline consumer shutdown: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("deadline consumer did not drain")
		}
	})

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
		const correlation = "quarantine-correlation"
		type quarantinedDelivery struct {
			message     messaging.Message
			correlation string
		}
		quarantined := make(chan quarantinedDelivery, 1)

		quarantineCtx, stopQuarantine := context.WithCancel(ctx)
		quarantineErr := make(chan error, 1)
		go func() {
			cfg := workerConfig(stream, "quarantine-observer", "template.events.quarantine")
			cfg.QuarantineSubject = "template.events.quarantine.failed"
			quarantineErr <- client.RunConsumer(
				quarantineCtx,
				cfg,
				func(handlerCtx context.Context, msg messaging.Message) error {
					value, _ := handlerCtx.Value(testPropagationKey{}).(string)
					quarantined <- quarantinedDelivery{
						message:     msg,
						correlation: value,
					}
					return nil
				},
			)
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

		publishCtx := context.WithValue(ctx, testPropagationKey{}, correlation)
		if _, err := client.Publish(
			publishCtx,
			"template.events.fail",
			"fail-1",
			[]byte("quarantine-me"),
		); err != nil {
			t.Fatalf("publish failing message: %v", err)
		}

		select {
		case delivery := <-quarantined:
			if string(delivery.message.Data) != "quarantine-me" {
				t.Fatalf("quarantine payload = %q", delivery.message.Data)
			}
			if delivery.correlation != correlation {
				t.Fatalf(
					"quarantine correlation = %q, want %q",
					delivery.correlation,
					correlation,
				)
			}
		case <-time.After(8 * time.Second):
			t.Fatal("timed out waiting for quarantine message")
		}

		adminConn, err := nats.Connect(natsURL, nats.Timeout(2*time.Second))
		if err != nil {
			t.Fatalf("open quarantine-budget admin connection: %v", err)
		}
		defer adminConn.Close()
		adminJS, err := jetstream.New(adminConn)
		if err != nil {
			t.Fatalf("create quarantine-budget admin client: %v", err)
		}
		sourceConsumer, err := adminJS.Consumer(ctx, stream.Name, "failing-worker")
		if err != nil {
			t.Fatalf("load source consumer: %v", err)
		}
		sourceInfo := sourceConsumer.CachedInfo()
		if sourceInfo == nil {
			t.Fatal("source consumer info is unavailable")
		}
		if got := sourceInfo.Config.MaxDeliver; got != 2 {
			t.Fatalf("source MaxDeliver = %d, want 2 for process=1 quarantine=2", got)
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

	t.Run("refuses durable consumer configuration drift", func(t *testing.T) {
		cfg := workerConfig(stream, "drift-worker", "template.events.consumer-drift")

		adminConn, err := nats.Connect(natsURL, nats.Timeout(2*time.Second))
		if err != nil {
			t.Fatalf("open consumer admin nats connection: %v", err)
		}
		defer adminConn.Close()

		adminJS, err := jetstream.New(adminConn)
		if err != nil {
			t.Fatalf("create consumer admin jetstream client: %v", err)
		}

		adminConsumer, err := adminJS.CreateConsumer(
			ctx,
			stream.Name,
			jetstream.ConsumerConfig{
				Durable:       cfg.Durable,
				Description:   "operator-owned description",
				DeliverPolicy: jetstream.DeliverAllPolicy,
				AckPolicy:     jetstream.AckExplicitPolicy,
				AckWait:       cfg.AckWait,
				MaxDeliver:    cfg.ProcessAttempts + cfg.QuarantineAttempts - 1,
				FilterSubject: cfg.FilterSubject,
				ReplayPolicy:  jetstream.ReplayInstantPolicy,
				MaxAckPending: cfg.MaxAckPending,
			},
		)
		if err != nil {
			t.Fatalf("create admin durable consumer: %v", err)
		}
		info := adminConsumer.CachedInfo()
		if info == nil {
			t.Fatal("admin consumer info is unavailable")
		}
		baselineConfig := info.Config

		driftedConfig := baselineConfig
		driftedConfig.MaxAckPending = cfg.MaxAckPending + 1
		if _, err := adminJS.UpdateConsumer(ctx, stream.Name, driftedConfig); err != nil {
			t.Fatalf("drift durable consumer through admin API: %v", err)
		}

		err = client.RunConsumer(
			ctx,
			cfg,
			func(context.Context, messaging.Message) error { return nil },
		)
		if !errors.Is(err, messaging.ErrConsumerConfigDrift) {
			t.Fatalf(
				"RunConsumer() drift error = %v, want messaging.ErrConsumerConfigDrift",
				err,
			)
		}

		driftedConsumer, err := adminJS.Consumer(ctx, stream.Name, cfg.Durable)
		if err != nil {
			t.Fatalf("reload drifted durable consumer: %v", err)
		}
		driftedInfo := driftedConsumer.CachedInfo()
		if driftedInfo == nil {
			t.Fatal("drifted consumer info is unavailable")
		}
		if driftedInfo.Config.MaxAckPending != driftedConfig.MaxAckPending {
			t.Fatalf(
				"RunConsumer() silently reconciled MaxAckPending to %d; want drifted %d",
				driftedInfo.Config.MaxAckPending,
				driftedConfig.MaxAckPending,
			)
		}

		if _, err := adminJS.UpdateConsumer(ctx, stream.Name, baselineConfig); err != nil {
			t.Fatalf("restore durable consumer through admin API: %v", err)
		}

		acceptedCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		defer cancel()
		if err := client.RunConsumer(
			acceptedCtx,
			cfg,
			func(context.Context, messaging.Message) error { return nil },
		); err != nil {
			t.Fatalf("RunConsumer() after explicit consumer restore: %v", err)
		}
	})

	if err := client.ReadinessCheck(stream, time.Second)(ctx); err != nil {
		t.Fatalf("nats readiness: %v", err)
	}

	adminConn, err := nats.Connect(natsURL, nats.Timeout(2*time.Second))
	if err != nil {
		t.Fatalf("open admin nats connection: %v", err)
	}
	defer adminConn.Close()
	adminJS, err := jetstream.New(adminConn)
	if err != nil {
		t.Fatalf("create admin jetstream client: %v", err)
	}
	adminStream, err := adminJS.Stream(ctx, stream.Name)
	if err != nil {
		t.Fatalf("load stream for admin drift test: %v", err)
	}
	baselineInfo := adminStream.CachedInfo()
	if baselineInfo == nil {
		t.Fatal("admin stream info is unavailable")
	}
	baselineConfig := baselineInfo.Config

	driftedConfig := baselineConfig
	driftedConfig.MaxBytes = stream.MaxBytes / 2
	if _, err := adminJS.UpdateStream(ctx, driftedConfig); err != nil {
		t.Fatalf("drift stream configuration through admin API: %v", err)
	}
	if err := client.ReadinessCheck(stream, time.Second)(ctx); err == nil {
		t.Fatal("nats readiness succeeded after managed stream configuration drift")
	}

	err = client.EnsureStream(ctx, stream)
	if !errors.Is(err, messaging.ErrStreamConfigDrift) {
		t.Fatalf(
			"EnsureStream() drift error = %v, want messaging.ErrStreamConfigDrift",
			err,
		)
	}
	if err := client.ReadinessCheck(stream, time.Second)(ctx); err == nil {
		t.Fatal("EnsureStream() silently reconciled existing stream drift")
	}

	if _, err := adminJS.UpdateStream(ctx, baselineConfig); err != nil {
		t.Fatalf("restore stream configuration through admin API: %v", err)
	}
	if err := client.ReadinessCheck(stream, time.Second)(ctx); err != nil {
		t.Fatalf("nats readiness after explicit stream restore: %v", err)
	}

	if err := adminJS.DeleteStream(ctx, stream.Name); err != nil {
		t.Fatalf("delete required stream: %v", err)
	}
	if err := client.ReadinessCheck(stream, time.Second)(ctx); err == nil {
		t.Fatal("nats readiness succeeded after required stream deletion")
	}
}

func workerConfig(stream messaging.StreamConfig, durable, filter string) messaging.ConsumerConfig {
	return messaging.ConsumerConfig{
		Stream:             stream,
		Durable:            durable,
		FilterSubject:      filter,
		QuarantineSubject:  "template.events.quarantine",
		AckWait:            5 * time.Second,
		ProcessAttempts:    2,
		QuarantineAttempts: 2,
		MaxAckPending:      8,
		RetryDelay:         100 * time.Millisecond,
		HandlerTimeout:     time.Second,
		AckTimeout:         time.Second,
		PullExpiry:         time.Second,
	}
}
