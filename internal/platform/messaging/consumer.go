package messaging

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

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
	if strings.TrimSpace(c.FilterSubject) == "" || strings.TrimSpace(c.QuarantineSubject) == "" {
		return fmt.Errorf("consumer filter and quarantine subjects must not be empty")
	}
	if c.FilterSubject == c.QuarantineSubject {
		return fmt.Errorf("consumer filter and quarantine subject must differ")
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
	if err := c.EnsureStream(ctx, cfg.Stream); err != nil {
		return err
	}

	requestCtx, cancel := context.WithTimeout(ctx, c.requestTimeout)
	consumer, err := c.js.CreateOrUpdateConsumer(requestCtx, cfg.Stream.Name, jetstream.ConsumerConfig{
		Durable:       cfg.Durable,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       cfg.AckWait,
		MaxDeliver:    cfg.ProcessAttempts + cfg.QuarantineAttempts,
		MaxAckPending: cfg.MaxAckPending,
		FilterSubject: cfg.FilterSubject,
	})
	cancel()
	if err != nil {
		return newOperationError("create or update jetstream consumer", err)
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
			c.handleDelivery(workCtx, cfg, msg, handler, reportFatal)
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

func (c *Client) handleDelivery(
	parent context.Context,
	cfg ConsumerConfig,
	msg jetstream.Msg,
	handler Handler,
	reportFatal func(error),
) {
	metadata, err := msg.Metadata()
	if err != nil {
		reportFatal(newOperationError("read jetstream message metadata", err))
		return
	}

	if metadata.NumDelivered > uint64(cfg.ProcessAttempts) {
		c.quarantine(parent, cfg, msg, metadata, reportFatal)
		return
	}

	handlerCtx, cancel := context.WithTimeout(parent, cfg.HandlerTimeout)
	handlerErr := handler(handlerCtx, Message{
		Subject:      msg.Subject(),
		Data:         append([]byte(nil), msg.Data()...),
		NumDelivered: metadata.NumDelivered,
	})
	cancel()

	if handlerErr == nil {
		if err := doubleAck(parent, msg, cfg.AckTimeout); err != nil {
			reportFatal(err)
		}
		return
	}

	if metadata.NumDelivered < uint64(cfg.ProcessAttempts) {
		if err := msg.NakWithDelay(cfg.RetryDelay); err != nil {
			reportFatal(newOperationError("schedule jetstream retry", err))
		}
		return
	}

	c.quarantine(parent, cfg, msg, metadata, reportFatal)
}

func (c *Client) quarantine(
	parent context.Context,
	cfg ConsumerConfig,
	msg jetstream.Msg,
	metadata *jetstream.MsgMetadata,
	reportFatal func(error),
) {
	quarantineCtx, cancel := context.WithTimeout(parent, c.requestTimeout)
	defer cancel()

	out := nats.NewMsg(cfg.QuarantineSubject)
	out.Data = append([]byte(nil), msg.Data()...)
	out.Header.Set("X-Original-Subject", msg.Subject())
	out.Header.Set("X-Original-Stream", metadata.Stream)
	out.Header.Set("X-Original-Sequence", strconv.FormatUint(metadata.Sequence.Stream, 10))
	out.Header.Set("X-Delivery-Count", strconv.FormatUint(metadata.NumDelivered, 10))

	msgID := fmt.Sprintf("quarantine:%s:%d", metadata.Stream, metadata.Sequence.Stream)
	if _, err := c.js.PublishMsg(quarantineCtx, out, jetstream.WithMsgID(msgID)); err != nil {
		totalAttempts := uint64(cfg.ProcessAttempts) + uint64(cfg.QuarantineAttempts)
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

func doubleAck(parent context.Context, msg jetstream.Msg, timeout time.Duration) error {
	ackCtx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	if err := msg.DoubleAck(ackCtx); err != nil {
		return newOperationError("ack jetstream message", err)
	}
	return nil
}
