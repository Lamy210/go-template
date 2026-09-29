package messaging

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// StreamConfig defines an explicitly bounded JetStream stream.
type StreamConfig struct {
	Name            string
	Subjects        []string
	MaxConsumers    int
	MaxMessages     int64
	MaxBytes        int64
	MaxAge          time.Duration
	MaxMessageSize  int32
	DuplicateWindow time.Duration
}

// Validate rejects JetStream's otherwise-unlimited size/count defaults.
func (c StreamConfig) Validate() error {
	if strings.TrimSpace(c.Name) == "" {
		return fmt.Errorf("stream name must not be empty")
	}
	if len(c.Subjects) == 0 {
		return fmt.Errorf("stream subjects must not be empty")
	}
	if c.MaxConsumers <= 0 || c.MaxMessages <= 0 || c.MaxBytes <= 0 || c.MaxMessageSize <= 0 {
		return fmt.Errorf("stream limits must be positive")
	}
	if c.MaxAge <= 0 || c.DuplicateWindow <= 0 {
		return fmt.Errorf("stream time limits must be positive")
	}
	return nil
}

var errManagedStreamConfigDrift = errors.New("managed stream configuration differs")

// EnsureStream creates the bounded file-backed JetStream stream when it is
// missing. An existing stream must already match the managed configuration;
// startup never mutates an existing stream implicitly.
func (c *Client) EnsureStream(ctx context.Context, cfg StreamConfig) error {
	if err := cfg.Validate(); err != nil {
		return err
	}

	requestCtx, cancel := context.WithTimeout(ctx, c.requestTimeout)
	defer cancel()

	stream, err := c.js.CreateStream(requestCtx, managedStreamConfig(cfg))
	if err != nil {
		if errors.Is(err, jetstream.ErrStreamNameAlreadyInUse) {
			return newOperationError(
				"required jetstream stream configuration drift",
				errors.Join(errManagedStreamConfigDrift, err),
			)
		}
		return newOperationError("create jetstream stream", err)
	}

	info := stream.CachedInfo()
	if info == nil {
		return newOperationError(
			"read required jetstream stream configuration",
			errors.New("stream information unavailable"),
		)
	}
	if !managedStreamConfigMatches(info.Config, cfg) {
		return newOperationError(
			"required jetstream stream configuration drift",
			errManagedStreamConfigDrift,
		)
	}
	return nil
}

func managedStreamConfig(cfg StreamConfig) jetstream.StreamConfig {
	return jetstream.StreamConfig{
		Name:         cfg.Name,
		Subjects:     append([]string(nil), cfg.Subjects...),
		Retention:    jetstream.LimitsPolicy,
		MaxConsumers: cfg.MaxConsumers,
		MaxMsgs:      cfg.MaxMessages,
		MaxBytes:     cfg.MaxBytes,
		Discard:      jetstream.DiscardOld,
		MaxAge:       cfg.MaxAge,
		MaxMsgSize:   cfg.MaxMessageSize,
		Storage:      jetstream.FileStorage,
		Replicas:     1,
		Duplicates:   cfg.DuplicateWindow,
	}
}

func managedStreamConfigMatches(actual jetstream.StreamConfig, desired StreamConfig) bool {
	expected := managedStreamConfig(desired)

	actualSubjects := append([]string(nil), actual.Subjects...)
	expectedSubjects := append([]string(nil), expected.Subjects...)
	slices.Sort(actualSubjects)
	slices.Sort(expectedSubjects)

	return actual.Name == expected.Name &&
		slices.Equal(actualSubjects, expectedSubjects) &&
		actual.Retention == expected.Retention &&
		actual.MaxConsumers == expected.MaxConsumers &&
		actual.MaxMsgs == expected.MaxMsgs &&
		actual.MaxBytes == expected.MaxBytes &&
		actual.Discard == expected.Discard &&
		actual.MaxAge == expected.MaxAge &&
		actual.MaxMsgSize == expected.MaxMsgSize &&
		actual.Storage == expected.Storage &&
		actual.Replicas == expected.Replicas &&
		actual.Duplicates == expected.Duplicates &&
		!actual.NoAck &&
		!actual.Sealed
}
