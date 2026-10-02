package config

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/Lamy210/go-template/internal/natsbudget"
	"github.com/Lamy210/go-template/internal/natsname"
	"github.com/Lamy210/go-template/internal/natssubject"
)

const (
	defaultNATSEnabled            = false
	defaultNATSURL                = "nats://127.0.0.1:4222"
	defaultNATSConnectTimeout     = 2 * time.Second
	defaultNATSReconnectWait      = 2 * time.Second
	defaultNATSMaxReconnects      = 30
	defaultNATSDrainTimeout       = 15 * time.Second
	defaultNATSRequestTimeout     = 5 * time.Second
	defaultNATSStream             = "APP_EVENTS"
	defaultNATSSubjects           = "app.events.>"
	defaultNATSMaxConsumers       = 16
	defaultNATSMaxMessages        = int64(100_000)
	defaultNATSMaxBytes           = int64(1 << 30)
	defaultNATSMaxAge             = 7 * 24 * time.Hour
	defaultNATSMaxMessageSize     = int32(1 << 20)
	defaultNATSDuplicateWindow    = 2 * time.Minute
	defaultNATSDurable            = "app-worker"
	defaultNATSFilterSubject      = "app.events.work"
	defaultNATSQuarantineSubject  = "app.events.quarantine"
	defaultNATSAckWait            = 45 * time.Second
	defaultNATSProcessAttempts    = 3
	defaultNATSQuarantineAttempts = 2
	defaultNATSMaxAckPending      = 128
	defaultNATSRetryDelay         = 5 * time.Second
	defaultNATSHandlerTimeout     = 30 * time.Second
	defaultNATSAckTimeout         = 5 * time.Second
	defaultNATSPullExpiry         = 5 * time.Second
)

// NATSConfig contains the optional NATS JetStream profile configuration.
type NATSConfig struct {
	Enabled            bool
	URL                string
	ConnectTimeout     time.Duration
	ReconnectWait      time.Duration
	MaxReconnects      int
	DrainTimeout       time.Duration
	RequestTimeout     time.Duration
	Stream             string
	Subjects           []string
	MaxConsumers       int
	MaxMessages        int64
	MaxBytes           int64
	MaxAge             time.Duration
	MaxMessageSize     int32
	DuplicateWindow    time.Duration
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

func loadNATS(lookup lookupEnv) (NATSConfig, error) {
	enabled, err := boolValue(lookup, "NATS_ENABLED", defaultNATSEnabled)
	if err != nil {
		return NATSConfig{}, err
	}

	cfg := defaultNATSConfig()
	cfg.Enabled = enabled
	if !enabled {
		return cfg, nil
	}

	connectTimeout, err := durationValue(lookup, "NATS_CONNECT_TIMEOUT", defaultNATSConnectTimeout)
	if err != nil {
		return NATSConfig{}, err
	}
	reconnectWait, err := durationValue(lookup, "NATS_RECONNECT_WAIT", defaultNATSReconnectWait)
	if err != nil {
		return NATSConfig{}, err
	}
	maxReconnects, err := intValue(lookup, "NATS_MAX_RECONNECTS", defaultNATSMaxReconnects)
	if err != nil {
		return NATSConfig{}, err
	}
	drainTimeout, err := durationValue(lookup, "NATS_DRAIN_TIMEOUT", defaultNATSDrainTimeout)
	if err != nil {
		return NATSConfig{}, err
	}
	requestTimeout, err := durationValue(lookup, "NATS_REQUEST_TIMEOUT", defaultNATSRequestTimeout)
	if err != nil {
		return NATSConfig{}, err
	}
	maxConsumers, err := intValue(lookup, "NATS_STREAM_MAX_CONSUMERS", defaultNATSMaxConsumers)
	if err != nil {
		return NATSConfig{}, err
	}
	maxMessages, err := int64Value(lookup, "NATS_STREAM_MAX_MESSAGES", defaultNATSMaxMessages)
	if err != nil {
		return NATSConfig{}, err
	}
	maxBytes, err := int64Value(lookup, "NATS_STREAM_MAX_BYTES", defaultNATSMaxBytes)
	if err != nil {
		return NATSConfig{}, err
	}
	maxAge, err := durationValue(lookup, "NATS_STREAM_MAX_AGE", defaultNATSMaxAge)
	if err != nil {
		return NATSConfig{}, err
	}
	maxMessageSize, err := int32Value(lookup, "NATS_STREAM_MAX_MESSAGE_SIZE", defaultNATSMaxMessageSize)
	if err != nil {
		return NATSConfig{}, err
	}
	duplicateWindow, err := durationValue(lookup, "NATS_DUPLICATE_WINDOW", defaultNATSDuplicateWindow)
	if err != nil {
		return NATSConfig{}, err
	}
	ackWait, err := durationValue(lookup, "NATS_ACK_WAIT", defaultNATSAckWait)
	if err != nil {
		return NATSConfig{}, err
	}
	processAttempts, err := intValue(lookup, "NATS_PROCESS_ATTEMPTS", defaultNATSProcessAttempts)
	if err != nil {
		return NATSConfig{}, err
	}
	quarantineAttempts, err := intValue(lookup, "NATS_QUARANTINE_ATTEMPTS", defaultNATSQuarantineAttempts)
	if err != nil {
		return NATSConfig{}, err
	}
	maxAckPending, err := intValue(lookup, "NATS_MAX_ACK_PENDING", defaultNATSMaxAckPending)
	if err != nil {
		return NATSConfig{}, err
	}
	retryDelay, err := durationValue(lookup, "NATS_RETRY_DELAY", defaultNATSRetryDelay)
	if err != nil {
		return NATSConfig{}, err
	}
	handlerTimeout, err := durationValue(lookup, "NATS_HANDLER_TIMEOUT", defaultNATSHandlerTimeout)
	if err != nil {
		return NATSConfig{}, err
	}
	ackTimeout, err := durationValue(lookup, "NATS_ACK_TIMEOUT", defaultNATSAckTimeout)
	if err != nil {
		return NATSConfig{}, err
	}
	pullExpiry, err := durationValue(lookup, "NATS_PULL_EXPIRY", defaultNATSPullExpiry)
	if err != nil {
		return NATSConfig{}, err
	}

	cfg.URL = stringValue(lookup, "NATS_URL", defaultNATSURL)
	cfg.ConnectTimeout = connectTimeout
	cfg.ReconnectWait = reconnectWait
	cfg.MaxReconnects = maxReconnects
	cfg.DrainTimeout = drainTimeout
	cfg.RequestTimeout = requestTimeout
	cfg.Stream = stringValue(lookup, "NATS_STREAM", defaultNATSStream)
	cfg.Subjects = splitCSV(stringValue(lookup, "NATS_SUBJECTS", defaultNATSSubjects))
	cfg.MaxConsumers = maxConsumers
	cfg.MaxMessages = maxMessages
	cfg.MaxBytes = maxBytes
	cfg.MaxAge = maxAge
	cfg.MaxMessageSize = maxMessageSize
	cfg.DuplicateWindow = duplicateWindow
	cfg.Durable = stringValue(lookup, "NATS_DURABLE", defaultNATSDurable)
	cfg.FilterSubject = stringValue(lookup, "NATS_FILTER_SUBJECT", defaultNATSFilterSubject)
	cfg.QuarantineSubject = stringValue(lookup, "NATS_QUARANTINE_SUBJECT", defaultNATSQuarantineSubject)
	cfg.AckWait = ackWait
	cfg.ProcessAttempts = processAttempts
	cfg.QuarantineAttempts = quarantineAttempts
	cfg.MaxAckPending = maxAckPending
	cfg.RetryDelay = retryDelay
	cfg.HandlerTimeout = handlerTimeout
	cfg.AckTimeout = ackTimeout
	cfg.PullExpiry = pullExpiry

	if err := cfg.Validate(); err != nil {
		return NATSConfig{}, err
	}
	return cfg, nil
}

func defaultNATSConfig() NATSConfig {
	return NATSConfig{
		Enabled:            defaultNATSEnabled,
		URL:                defaultNATSURL,
		ConnectTimeout:     defaultNATSConnectTimeout,
		ReconnectWait:      defaultNATSReconnectWait,
		MaxReconnects:      defaultNATSMaxReconnects,
		DrainTimeout:       defaultNATSDrainTimeout,
		RequestTimeout:     defaultNATSRequestTimeout,
		Stream:             defaultNATSStream,
		Subjects:           splitCSV(defaultNATSSubjects),
		MaxConsumers:       defaultNATSMaxConsumers,
		MaxMessages:        defaultNATSMaxMessages,
		MaxBytes:           defaultNATSMaxBytes,
		MaxAge:             defaultNATSMaxAge,
		MaxMessageSize:     defaultNATSMaxMessageSize,
		DuplicateWindow:    defaultNATSDuplicateWindow,
		Durable:            defaultNATSDurable,
		FilterSubject:      defaultNATSFilterSubject,
		QuarantineSubject:  defaultNATSQuarantineSubject,
		AckWait:            defaultNATSAckWait,
		ProcessAttempts:    defaultNATSProcessAttempts,
		QuarantineAttempts: defaultNATSQuarantineAttempts,
		MaxAckPending:      defaultNATSMaxAckPending,
		RetryDelay:         defaultNATSRetryDelay,
		HandlerTimeout:     defaultNATSHandlerTimeout,
		AckTimeout:         defaultNATSAckTimeout,
		PullExpiry:         defaultNATSPullExpiry,
	}
}

// Validate rejects unbounded or incomplete enabled NATS profiles.
func (c NATSConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	if strings.TrimSpace(c.URL) == "" {
		return fmt.Errorf("NATS_URL must not be empty when NATS_ENABLED=true")
	}
	if c.ConnectTimeout <= 0 {
		return fmt.Errorf("NATS_CONNECT_TIMEOUT must be positive")
	}
	if c.ReconnectWait <= 0 {
		return fmt.Errorf("NATS_RECONNECT_WAIT must be positive")
	}
	if c.MaxReconnects < 0 {
		return fmt.Errorf("NATS_MAX_RECONNECTS must not be negative")
	}
	if c.DrainTimeout <= 0 || c.RequestTimeout <= 0 {
		return fmt.Errorf("NATS drain and request timeouts must be positive")
	}
	if strings.TrimSpace(c.Stream) == "" || len(c.Subjects) == 0 {
		return fmt.Errorf("NATS stream and subjects must not be empty")
	}
	if err := natsname.Validate(c.Stream); err != nil {
		return fmt.Errorf("NATS_STREAM is invalid: %w", err)
	}
	for _, subject := range c.Subjects {
		if err := natssubject.ValidatePattern(subject); err != nil {
			return fmt.Errorf("NATS_SUBJECTS contains an invalid subject pattern: %w", err)
		}
	}
	if c.MaxConsumers <= 0 || c.MaxMessages <= 0 || c.MaxBytes <= 0 || c.MaxMessageSize <= 0 {
		return fmt.Errorf("NATS stream limits must be positive")
	}
	if c.MaxAge <= 0 || c.DuplicateWindow <= 0 {
		return fmt.Errorf("NATS stream time limits must be positive")
	}
	if strings.TrimSpace(c.Durable) == "" || strings.TrimSpace(c.FilterSubject) == "" || strings.TrimSpace(c.QuarantineSubject) == "" {
		return fmt.Errorf("NATS consumer names and subjects must not be empty")
	}
	if err := natsname.Validate(c.Durable); err != nil {
		return fmt.Errorf("NATS_DURABLE is invalid: %w", err)
	}
	if err := natssubject.ValidatePattern(c.FilterSubject); err != nil {
		return fmt.Errorf("NATS_FILTER_SUBJECT is invalid: %w", err)
	}
	if err := natssubject.ValidateLiteral(c.QuarantineSubject); err != nil {
		return fmt.Errorf("NATS_QUARANTINE_SUBJECT is invalid: %w", err)
	}
	if natssubject.PatternMatchesLiteral(c.FilterSubject, c.QuarantineSubject) {
		return fmt.Errorf("NATS_FILTER_SUBJECT must not match NATS_QUARANTINE_SUBJECT")
	}
	if c.AckWait <= 0 || c.ProcessAttempts <= 0 || c.QuarantineAttempts <= 0 || c.MaxAckPending <= 0 {
		return fmt.Errorf("NATS consumer limits must be positive")
	}
	if c.ProcessAttempts > math.MaxInt-(c.QuarantineAttempts-1) {
		return fmt.Errorf("NATS delivery attempts exceed integer range")
	}
	if c.RetryDelay <= 0 || c.HandlerTimeout <= 0 || c.AckTimeout <= 0 {
		return fmt.Errorf("NATS worker timeouts must be positive")
	}
	if c.PullExpiry < time.Second {
		return fmt.Errorf("NATS_PULL_EXPIRY must be at least one second")
	}
	if !natsbudget.AckWaitCoversSettlement(
		c.AckWait,
		c.HandlerTimeout,
		c.RequestTimeout,
		c.AckTimeout,
	) {
		return fmt.Errorf(
			"NATS_ACK_WAIT must exceed NATS_HANDLER_TIMEOUT + NATS_REQUEST_TIMEOUT + NATS_ACK_TIMEOUT",
		)
	}
	return nil
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
