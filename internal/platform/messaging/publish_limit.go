package messaging

import (
	"errors"
	"sync"

	"github.com/Lamy210/go-template/internal/natssubject"
	"github.com/nats-io/nats.go"
)

const (
	natsHeaderPreambleBytes = int64(len("NATS/1.0\r\n") + len("\r\n"))
	natsHeaderFieldOverhead = int64(4) // colon, space, CR, LF
)

var (
	// ErrMessageTooLarge means the final header + payload size exceeds a known
	// broker or managed-stream publish limit.
	ErrMessageTooLarge = errors.New("nats message exceeds publish size limit")
	// ErrManagedStreamMessageTooLarge classifies a message that exceeds the
	// verified application-managed JetStream MaxMsgSize. Unlike the broker's
	// runtime MaxPayload, this bound is part of the managed stream contract.
	ErrManagedStreamMessageTooLarge = errors.New("nats message exceeds managed stream size limit")
)

type streamPublishLimit struct {
	subjects       []string
	maxMessageSize int64
}

type publishLimits struct {
	mu      sync.RWMutex
	streams map[string]streamPublishLimit
}

func (l *publishLimits) remember(name string, subjects []string, maxMessageSize int32) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.streams == nil {
		l.streams = make(map[string]streamPublishLimit)
	}
	l.streams[name] = streamPublishLimit{
		subjects:       append([]string(nil), subjects...),
		maxMessageSize: int64(maxMessageSize),
	}
}

func (l *publishLimits) forSubject(subject string, serverMaxPayload int64) int64 {
	return minPositiveLimit(
		positiveLimit(serverMaxPayload),
		l.managedForSubject(subject),
	)
}

func (l *publishLimits) managedForSubject(subject string) int64 {
	var limit int64

	l.mu.RLock()
	defer l.mu.RUnlock()

	for _, stream := range l.streams {
		if !matchesAnySubject(stream.subjects, subject) {
			continue
		}
		limit = minPositiveLimit(limit, stream.maxMessageSize)
	}
	return limit
}

func matchesAnySubject(patterns []string, subject string) bool {
	for _, pattern := range patterns {
		if natssubject.PatternMatchesLiteral(pattern, subject) {
			return true
		}
	}
	return false
}

func positiveLimit(value int64) int64 {
	if value <= 0 {
		return 0
	}
	return value
}

func minPositiveLimit(current, candidate int64) int64 {
	if candidate <= 0 {
		return current
	}
	if current <= 0 || candidate < current {
		return candidate
	}
	return current
}

// messageFitsPublishLimit uses the same conservative header-size upper bound as
// nats.go v1.54.0: NATS/1.0 framing plus key/value bytes and ": \r\n" per
// value. nats.go may trim header values before encoding, so this can
// over-estimate but must not under-estimate the wire header size.
func messageFitsPublishLimit(msg *nats.Msg, limit int64) bool {
	if msg == nil || limit <= 0 {
		return true
	}

	remaining := limit
	if len(msg.Header) > 0 {
		if !consumePublishBudget(&remaining, natsHeaderPreambleBytes) {
			return false
		}
		for key, values := range msg.Header {
			for _, value := range values {
				if !consumePublishBudget(&remaining, int64(len(key))) ||
					!consumePublishBudget(&remaining, int64(len(value))) ||
					!consumePublishBudget(&remaining, natsHeaderFieldOverhead) {
					return false
				}
			}
		}
	}

	return int64(len(msg.Data)) <= remaining
}

func consumePublishBudget(remaining *int64, size int64) bool {
	if size < 0 || size > *remaining {
		return false
	}
	*remaining -= size
	return true
}
