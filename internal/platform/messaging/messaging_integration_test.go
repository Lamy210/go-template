//go:build messaging

package messaging_test

import (
	"context"
	"errors"
	"os"
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

	if err := client.ReadinessCheck(stream, time.Second)(ctx); err != nil {
		t.Fatalf("nats readiness: %v", err)
	}

	driftedStream := stream
	driftedStream.MaxBytes = stream.MaxBytes / 2
	if err := client.EnsureStream(ctx, driftedStream); err != nil {
		t.Fatalf("drift stream configuration: %v", err)
	}
	if err := client.ReadinessCheck(stream, time.Second)(ctx); err == nil {
		t.Fatal("nats readiness succeeded after managed stream configuration drift")
	}
	if err := client.EnsureStream(ctx, stream); err != nil {
		t.Fatalf("restore stream configuration: %v", err)
	}
	if err := client.ReadinessCheck(stream, time.Second)(ctx); err != nil {
		t.Fatalf("nats readiness after stream restore: %v", err)
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
