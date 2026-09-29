package messaging

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/Lamy210/go-template/internal/natsname"
	"github.com/Lamy210/go-template/internal/natssubject"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Message is the application-facing subset of a JetStream delivery.
type Message struct {
	Subject      string
	Data         []byte
	NumDelivered uint64
}

// Handler processes one delivery. Returning an error requests bounded retry;
// the error text is never copied into quarantine messages or generic logs.
type Handler func(context.Context, Message) error

type handlerPanicError struct{}

func (handlerPanicError) Error() string {
	return "messaging handler panicked"
}

// ErrConsumerConfigDrift means an existing durable consumer differs in one or
// more fields managed by this template.
var ErrConsumerConfigDrift = errors.New("managed jetstream consumer configuration differs")

// ConsumerConfig configures one durable pull consumer and its retry/quarantine
// policy.
type ConsumerConfig struct {
	Stream             StreamConfig
	Durable            string
	FilterSubject      string
	QuarantineSubject  string
	AckWait            time.Duration
	ProcessAttempts    int
	QuarantineAttempts int
	MaxAckPending      int
	RetryDelay         time.Duration
	HandlerTimeout     time.Duration
	AckTimeout         time.Duration
	PullExpiry         time.Duration
}

// Validate rejects unlimited delivery and buffering behavior.
func (c ConsumerConfig) Validate() error {
	if err := c.Stream.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(c.Durable) == "" {
		return fmt.Errorf("consumer durable name must not be empty")
	}
	if err := natsname.Validate(c.Durable); err != nil {
		return fmt.Errorf("consumer durable name is invalid: %w", err)
	}
	if strings.TrimSpace(c.FilterSubject) == "" || strings.TrimSpace(c.QuarantineSubject) == "" {
		return fmt.Errorf("consumer filter and quarantine subjects must not be empty")
	}
	if err := natssubject.ValidatePattern(c.FilterSubject); err != nil {
		return fmt.Errorf("consumer filter subject is invalid: %w", err)
	}
	if err := natssubject.ValidateLiteral(c.QuarantineSubject); err != nil {
		return fmt.Errorf("consumer quarantine subject is invalid: %w", err)
	}
	if natssubject.PatternMatchesLiteral(c.FilterSubject, c.QuarantineSubject) {
		return fmt.Errorf("consumer filter subject must not match quarantine subject")
	}
	if c.AckWait <= 0 || c.ProcessAttempts <= 0 || c.QuarantineAttempts <= 0 || c.MaxAckPending <= 0 {
		return fmt.Errorf("consumer limits must be positive")
	}
	if c.ProcessAttempts > math.MaxInt-c.QuarantineAttempts {
		return fmt.Errorf("consumer delivery attempts exceed integer range")
	}
	if c.RetryDelay <= 0 || c.HandlerTimeout <= 0 || c.AckTimeout <= 0 || c.PullExpiry < time.Second {
		return fmt.Errorf("consumer timeouts must be positive and pull expiry must be at least one second")
	}
	if c.HandlerTimeout >= c.AckWait ||
		c.AckTimeout >= c.AckWait-c.HandlerTimeout {
		return fmt.Errorf("consumer ack wait must exceed handler timeout plus ack timeout")
	}
	return nil
}

// RunConsumer ensures the stream/consumer and blocks until ctx is canceled or
// an infrastructure failure occurs.
func (c *Client) RunConsumer(ctx context.Context, cfg ConsumerConfig, handler Handler) error {
	if handler == nil {
		return fmt.Errorf("consumer handler must not be nil")
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	processAttempts, totalAttempts, err := deliveryAttemptLimits(cfg)
	if err != nil {
		return err
	}
	if err := c.EnsureStream(ctx, cfg.Stream); err != nil {
		return err
	}

	consumer, err := c.ensureConsumer(ctx, cfg)
	if err != nil {
		return err
	}

	fatal := make(chan error, 1)
	reportFatal := func(err error) {
		if err == nil {
			return
		}
		select {
		case fatal <- err:
		default:
		}
	}

	// Preserve request-scoped values but detach cancellation for already-buffered
	// work. Shutdown stops new deliveries via ConsumeContext.Drain while each
	// in-flight handler remains bounded by HandlerTimeout.
	workCtx := context.WithoutCancel(ctx)
	consumeCtx, err := consumer.Consume(
		func(msg jetstream.Msg) {
			c.handleDelivery(
				workCtx,
				cfg,
				processAttempts,
				totalAttempts,
				msg,
				handler,
				reportFatal,
			)
		},
		jetstream.PullMaxMessages(cfg.MaxAckPending),
		jetstream.PullExpiry(cfg.PullExpiry),
		jetstream.ConsumeErrHandler(func(_ jetstream.ConsumeContext, err error) {
			reportFatal(newOperationError("consume jetstream messages", err))
		}),
	)
	if err != nil {
		return newOperationError("start jetstream consumer", err)
	}

	select {
	case <-ctx.Done():
		consumeCtx.Drain()
		drainCtx, cancel := context.WithTimeout(context.Background(), cfg.HandlerTimeout+cfg.AckTimeout)
		defer cancel()
		select {
		case <-consumeCtx.Closed():
			return nil
		case <-drainCtx.Done():
			consumeCtx.Stop()
			return newOperationError("drain jetstream consumer", drainCtx.Err())
		}
	case err := <-fatal:
		consumeCtx.Stop()
		stopCtx, cancel := context.WithTimeout(
			context.Background(),
			cfg.HandlerTimeout+cfg.AckTimeout,
		)
		defer cancel()

		select {
		case <-consumeCtx.Closed():
			return err
		case <-stopCtx.Done():
			return errors.Join(
				err,
				newOperationError("stop jetstream consumer", stopCtx.Err()),
			)
		}
	}
}

func (c *Client) ensureConsumer(
	ctx context.Context,
	cfg ConsumerConfig,
) (jetstream.Consumer, error) {
	requestCtx, cancel := context.WithTimeout(ctx, c.requestTimeout)
	defer cancel()

	consumer, err := c.js.Consumer(requestCtx, cfg.Stream.Name, cfg.Durable)
	if err == nil {
		return verifyManagedConsumer(consumer, cfg)
	}
	if !errors.Is(err, jetstream.ErrConsumerNotFound) {
		return nil, newOperationError("query durable jetstream consumer", err)
	}

	consumer, err = c.js.CreateConsumer(
		requestCtx,
		cfg.Stream.Name,
		managedConsumerConfig(cfg),
	)
	if err != nil {
		if !errors.Is(err, jetstream.ErrConsumerExists) {
			return nil, newOperationError("create durable jetstream consumer", err)
		}

		// Another instance may have created the durable between lookup and
		// create. Re-read it and apply the same managed-field verification.
		consumer, err = c.js.Consumer(requestCtx, cfg.Stream.Name, cfg.Durable)
		if err != nil {
			return nil, newOperationError(
				"query concurrently created jetstream consumer",
				err,
			)
		}
	}

	return verifyManagedConsumer(consumer, cfg)
}

func verifyManagedConsumer(
	consumer jetstream.Consumer,
	cfg ConsumerConfig,
) (jetstream.Consumer, error) {
	info := consumer.CachedInfo()
	if info == nil {
		return nil, newOperationError(
			"read durable jetstream consumer configuration",
			errors.New("consumer information unavailable"),
		)
	}
	if !managedConsumerConfigMatches(info.Config, cfg) {
		return nil, newOperationError(
			"durable jetstream consumer configuration drift",
			ErrConsumerConfigDrift,
		)
	}
	return consumer, nil
}

func managedConsumerConfig(cfg ConsumerConfig) jetstream.ConsumerConfig {
	return jetstream.ConsumerConfig{
		Durable:       cfg.Durable,
		DeliverPolicy: jetstream.DeliverAllPolicy,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       cfg.AckWait,
		MaxDeliver:    cfg.ProcessAttempts + cfg.QuarantineAttempts,
		FilterSubject: cfg.FilterSubject,
		ReplayPolicy:  jetstream.ReplayInstantPolicy,
		MaxAckPending: cfg.MaxAckPending,
	}
}

func managedConsumerConfigMatches(
	actual jetstream.ConsumerConfig,
	desired ConsumerConfig,
) bool {
	expected := managedConsumerConfig(desired)

	return actual.Durable == expected.Durable &&
		actual.DeliverPolicy == expected.DeliverPolicy &&
		actual.AckPolicy == expected.AckPolicy &&
		actual.AckWait == expected.AckWait &&
		actual.MaxDeliver == expected.MaxDeliver &&
		len(actual.BackOff) == 0 &&
		actual.FilterSubject == expected.FilterSubject &&
		len(actual.FilterSubjects) == 0 &&
		actual.ReplayPolicy == expected.ReplayPolicy &&
		actual.MaxAckPending == expected.MaxAckPending &&
		!actual.HeadersOnly &&
		actual.DeliverSubject == "" &&
		actual.DeliverGroup == ""
}

func (c *Client) handleDelivery(
	parent context.Context,
	cfg ConsumerConfig,
	processAttempts uint64,
	totalAttempts uint64,
	msg jetstream.Msg,
	handler Handler,
	reportFatal func(error),
) {
	metadata, err := msg.Metadata()
	if err != nil {
		reportFatal(newOperationError("read jetstream message metadata", err))
		return
	}

	deliveryCtx := parent
	if c.propagator != nil {
		deliveryCtx = c.propagator.Extract(
			parent,
			natsHeaderCarrier{header: msg.Headers()},
		)
	}

	if metadata.NumDelivered > processAttempts {
		c.quarantine(deliveryCtx, cfg, totalAttempts, msg, metadata, reportFatal)
		return
	}

	processCtx := deliveryCtx
	endOperation := func(error) {}
	if c.tracer != nil {
		processCtx, endOperation = c.tracer.StartProcess(deliveryCtx, msg.Subject())
		if processCtx == nil {
			processCtx = deliveryCtx
		}
		if endOperation == nil {
			endOperation = func(error) {}
		}
	}

	handlerCtx, cancel := context.WithTimeout(processCtx, cfg.HandlerTimeout)
	handlerErr := invokeHandler(handlerCtx, handler, Message{
		Subject:      msg.Subject(),
		Data:         append([]byte(nil), msg.Data()...),
		NumDelivered: metadata.NumDelivered,
	})
	cancel()

	if handlerErr == nil {
		ackErr := doubleAck(parent, msg, cfg.AckTimeout)
		endOperation(ackErr)
		if ackErr != nil {
			reportFatal(ackErr)
		}
		return
	}

	defer endOperation(handlerErr)

	if metadata.NumDelivered < processAttempts {
		if err := msg.NakWithDelay(cfg.RetryDelay); err != nil {
			reportFatal(newOperationError("schedule jetstream retry", err))
		}
		return
	}

	c.quarantine(processCtx, cfg, totalAttempts, msg, metadata, reportFatal)
}

func (c *Client) quarantine(
	parent context.Context,
	cfg ConsumerConfig,
	totalAttempts uint64,
	msg jetstream.Msg,
	metadata *jetstream.MsgMetadata,
	reportFatal func(error),
) {
	out := nats.NewMsg(cfg.QuarantineSubject)
	out.Data = append([]byte(nil), msg.Data()...)
	out.Header.Set("X-Original-Subject", msg.Subject())
	out.Header.Set("X-Original-Stream", metadata.Stream)
	out.Header.Set("X-Original-Sequence", strconv.FormatUint(metadata.Sequence.Stream, 10))
	out.Header.Set("X-Delivery-Count", strconv.FormatUint(metadata.NumDelivered, 10))

	msgID := fmt.Sprintf("quarantine:%s:%d", metadata.Stream, metadata.Sequence.Stream)
	if _, err := c.publishMessage(parent, out, msgID); err != nil {
		if metadata.NumDelivered < totalAttempts {
			if nakErr := msg.NakWithDelay(cfg.RetryDelay); nakErr != nil {
				reportFatal(newOperationError("schedule quarantine retry", nakErr))
			}
			return
		}
		reportFatal(newOperationError("publish quarantine message", err))
		return
	}

	if err := doubleAck(parent, msg, cfg.AckTimeout); err != nil {
		reportFatal(err)
	}
}

func invokeHandler(ctx context.Context, handler Handler, msg Message) (err error) {
	defer func() {
		if recover() != nil {
			err = handlerPanicError{}
		}
	}()

	if err := handler(ctx, msg); err != nil {
		return err
	}
	return ctx.Err()
}

func deliveryAttemptLimits(cfg ConsumerConfig) (uint64, uint64, error) {
	if cfg.ProcessAttempts < 0 || cfg.QuarantineAttempts < 0 {
		return 0, 0, fmt.Errorf("consumer delivery attempts must not be negative")
	}

	processAttempts := uint64(cfg.ProcessAttempts)
	quarantineAttempts := uint64(cfg.QuarantineAttempts)
	return processAttempts, processAttempts + quarantineAttempts, nil
}

func doubleAck(parent context.Context, msg jetstream.Msg, timeout time.Duration) error {
	ackCtx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	if err := msg.DoubleAck(ackCtx); err != nil {
		return newOperationError("ack jetstream message", err)
	}
	return nil
}
