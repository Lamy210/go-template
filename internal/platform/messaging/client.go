// Package messaging contains the optional NATS JetStream infrastructure profile.
package messaging

import (
	"context"
	"errors"
	"time"

	coreprop "github.com/Lamy210/go-template/internal/core/propagation"
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
	if c.URL == "" {
		return errors.New("nats URL must not be empty")
	}
	if c.Name == "" {
		return errors.New("nats client name must not be empty")
	}
	if c.ConnectTimeout <= 0 || c.ReconnectWait <= 0 || c.DrainTimeout <= 0 || c.RequestTimeout <= 0 {
		return errors.New("nats client timeouts must be positive")
	}
	if c.MaxReconnects < 0 {
		return errors.New("nats max reconnects must not be negative")
	}
	return nil
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

// Client owns a NATS connection and the modern JetStream API.
type Client struct {
	conn           *nats.Conn
	js             jetstream.JetStream
	requestTimeout time.Duration
	propagator     coreprop.TextMapPropagator
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
		if option != nil {
			option(client)
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
	publishCtx, cancel := context.WithTimeout(ctx, c.requestTimeout)
	defer cancel()

	msg := nats.NewMsg(subject)
	msg.Data = payload
	if c.propagator != nil {
		c.propagator.Inject(ctx, natsHeaderCarrier{header: msg.Header})
	}

	var options []jetstream.PublishOpt
	if msgID != "" {
		options = append(options, jetstream.WithMsgID(msgID))
	}
	ack, err := c.js.PublishMsg(publishCtx, msg, options...)
	if err != nil {
		return PublishAck{}, newOperationError("publish jetstream message", err)
	}

	return PublishAck{
		Stream:    ack.Stream,
		Sequence:  ack.Sequence,
		Duplicate: ack.Duplicate,
	}, nil
}

// PublishAck is the transport-neutral subset of a JetStream publish ack.
type PublishAck struct {
	Stream    string
	Sequence  uint64
	Duplicate bool
}

// ReadinessCheck verifies both the core connection and JetStream account API.
func (c *Client) ReadinessCheck(timeout time.Duration) func(context.Context) error {
	return func(ctx context.Context) error {
		if c.conn.Status() != nats.CONNECTED {
			return newOperationError("nats connection is not ready", errors.New("connection not connected"))
		}

		checkCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		if _, err := c.js.AccountInfo(checkCtx); err != nil {
			return newOperationError("query jetstream account", err)
		}
		return nil
	}
}

// Drain gracefully drains the NATS connection and waits for it to close.
// When ctx expires, the connection is forcibly closed.
func (c *Client) Drain(ctx context.Context) error {
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
