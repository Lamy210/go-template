// Package messaging contains the optional NATS JetStream infrastructure profile.
package messaging

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	coreprop "github.com/Lamy210/go-template/internal/core/propagation"
	"github.com/Lamy210/go-template/internal/messageid"
	"github.com/Lamy210/go-template/internal/natssubject"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// ClientConfig contains bounded NATS connection and JetStream API settings.
type ClientConfig struct {
	URL            string
	Name           string
	ConnectTimeout time.Duration
	ReconnectWait  time.Duration
	MaxReconnects  int
	DrainTimeout   time.Duration
	RequestTimeout time.Duration
}

// Validate rejects unbounded reconnects and missing connection limits.
func (c ClientConfig) Validate() error {
	if strings.TrimSpace(c.URL) == "" {
		return errors.New("nats URL must not be empty")
	}
	if strings.TrimSpace(c.Name) == "" {
		return errors.New("nats client name must not be empty")
	}
	if !utf8.ValidString(c.Name) {
		return errors.New("nats client name must be valid UTF-8")
	}
	if c.ConnectTimeout <= 0 || c.ReconnectWait <= 0 || c.DrainTimeout <= 0 || c.RequestTimeout <= 0 {
		return errors.New("nats client timeouts must be positive")
	}
	if c.MaxReconnects < 0 {
		return errors.New("nats max reconnects must not be negative")
	}
	return nil
}

// OperationTracer instruments logical messaging operations without coupling
// this package to a specific telemetry implementation.
type OperationTracer interface {
	StartPublish(context.Context, string) (context.Context, func(error))
	StartProcess(context.Context, string) (context.Context, func(error))
}

// Option configures optional messaging integrations without changing the
// bounded connection policy in ClientConfig.
type Option func(*Client)

// WithPropagator enables transport-neutral context injection/extraction.
func WithPropagator(propagator coreprop.TextMapPropagator) Option {
	return func(client *Client) {
		client.propagator = propagator
	}
}

// WithTracer enables logical publish/process span instrumentation.
func WithTracer(tracer OperationTracer) Option {
	return func(client *Client) {
		client.tracer = tracer
	}
}

func applyOptionSafely(client *Client, option Option) (err error) {
	if option == nil {
		return nil
	}
	defer func() {
		if recover() != nil {
			err = errClientOptionPanic
		}
	}()
	option(client)
	return nil
}

// Client owns a NATS connection and the modern JetStream API.
type Client struct {
	conn           *nats.Conn
	js             jetstream.JetStream
	requestTimeout time.Duration
	propagator     coreprop.TextMapPropagator
	tracer         OperationTracer
	publishLimits  publishLimits
}

func (c *Client) validateInitialized() error {
	if c == nil || c.conn == nil || c.js == nil || c.requestTimeout <= 0 {
		return errors.New("nats client must be initialized")
	}
	return nil
}

func (c *Client) validateConnection() error {
	if c == nil || c.conn == nil {
		return errors.New("nats client connection must be initialized")
	}
	return nil
}

// Open connects to NATS without retrying the initial startup connection.
// Subsequent reconnect attempts are bounded by MaxReconnects.
func Open(cfg ClientConfig, options ...Option) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	conn, err := nats.Connect(
		cfg.URL,
		nats.Name(cfg.Name),
		nats.Timeout(cfg.ConnectTimeout),
		nats.ReconnectWait(cfg.ReconnectWait),
		nats.MaxReconnects(cfg.MaxReconnects),
		nats.DrainTimeout(cfg.DrainTimeout),
		nats.RetryOnFailedConnect(false),
	)
	if err != nil {
		return nil, newOperationError("connect nats", err)
	}

	js, err := jetstream.New(conn, jetstream.WithDefaultTimeout(cfg.RequestTimeout))
	if err != nil {
		conn.Close()
		return nil, newOperationError("create jetstream client", err)
	}

	client := &Client{
		conn:           conn,
		js:             js,
		requestTimeout: cfg.RequestTimeout,
	}
	for _, option := range options {
		if err := applyOptionSafely(client, option); err != nil {
			conn.Close()
			return nil, newOperationError("configure nats client", err)
		}
	}
	return client, nil
}

// Close immediately closes the NATS connection.
//
// Callers should prefer Drain during a normal shutdown and keep Close as a
// fail-safe for startup failures or abnormal exits.
func (c *Client) Close() {
	if c == nil || c.conn == nil {
		return
	}
	c.conn.Close()
}

// Publish publishes synchronously and uses msgID for JetStream deduplication
// when it is non-empty.
func (c *Client) Publish(
	ctx context.Context,
	subject string,
	msgID string,
	payload []byte,
) (PublishAck, error) {
	if err := natssubject.ValidateLiteral(subject); err != nil {
		return PublishAck{}, newOperationError(
			"nats publish subject is invalid",
			errors.Join(ErrInvalidPublishSubject, err),
		)
	}
	if msgID != "" {
		if err := messageid.Validate(msgID); err != nil {
			return PublishAck{}, newOperationError(
				"nats message ID is invalid",
				errors.Join(ErrInvalidMessageID, err),
			)
		}
	}
	if err := c.validateInitialized(); err != nil {
		return PublishAck{}, err
	}

	msg := nats.NewMsg(subject)
	msg.Data = payload

	ack, err := c.publishMessage(ctx, msg, msgID)
	if err != nil {
		return PublishAck{}, newOperationError("publish jetstream message", err)
	}

	return PublishAck{
		Stream:    ack.Stream,
		Sequence:  ack.Sequence,
		Duplicate: ack.Duplicate,
	}, nil
}

func (c *Client) publishMessage(
	ctx context.Context,
	msg *nats.Msg,
	msgID string,
) (*jetstream.PubAck, error) {
	operationCtx, endOperation := c.startPublishOperationSafely(ctx, msg.Subject)
	c.injectPropagationSafely(operationCtx, msg.Header)
	if msgID != "" {
		// nats.go's WithMsgID writes the same Nats-Msg-Id header immediately
		// before sending. Materialize it here so size validation sees the final
		// transport headers before broker I/O.
		msg.Header.Set(jetstream.MsgIDHeader, msgID)
	}

	limit := c.publishLimits.forSubject(msg.Subject, c.conn.MaxPayload())
	if !messageFitsPublishLimit(msg, limit) {
		endOperation(ErrMessageTooLarge)
		return nil, ErrMessageTooLarge
	}

	publishCtx, cancel := context.WithTimeout(operationCtx, c.requestTimeout)
	defer cancel()

	ack, err := c.js.PublishMsg(publishCtx, msg)
	endOperation(err)
	return ack, err
}

// PublishAck is the transport-neutral subset of a JetStream publish ack.
type PublishAck struct {
	Stream    string
	Sequence  uint64
	Duplicate bool
}

// ReadinessCheck verifies the core connection and that the required JetStream
// stream still exists with the configuration fields managed by this template.
func (c *Client) ReadinessCheck(streamConfig StreamConfig, timeout time.Duration) func(context.Context) error {
	return func(ctx context.Context) error {
		if err := streamConfig.Validate(); err != nil {
			return err
		}
		if timeout <= 0 {
			return errors.New("nats readiness timeout must be positive")
		}
		if err := c.validateInitialized(); err != nil {
			return err
		}
		if c.conn.Status() != nats.CONNECTED {
			return newOperationError("nats connection is not ready", errors.New("connection not connected"))
		}

		checkCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		stream, err := c.js.Stream(checkCtx, streamConfig.Name)
		if err != nil {
			return newOperationError("query required jetstream stream", err)
		}
		info := stream.CachedInfo()
		if info == nil {
			return newOperationError(
				"read required jetstream stream configuration",
				errors.New("stream information unavailable"),
			)
		}
		if !managedStreamConfigMatches(info.Config, streamConfig) {
			return newOperationError(
				"required jetstream stream configuration drift",
				ErrStreamConfigDrift,
			)
		}
		return nil
	}
}

// Drain gracefully drains the NATS connection and waits for it to close.
// When ctx expires, the connection is forcibly closed.
func (c *Client) Drain(ctx context.Context) error {
	if err := c.validateConnection(); err != nil {
		return err
	}
	if c.conn.IsClosed() {
		return nil
	}
	if err := c.conn.Drain(); err != nil && !errors.Is(err, nats.ErrConnectionClosed) {
		return newOperationError("start nats drain", err)
	}

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		if c.conn.IsClosed() {
			if err := c.conn.LastError(); errors.Is(err, nats.ErrDrainTimeout) {
				return newOperationError("drain nats connection", err)
			}
			return nil
		}
		select {
		case <-ctx.Done():
			c.conn.Close()
			return newOperationError("wait for nats drain", ctx.Err())
		case <-ticker.C:
		}
	}
}
