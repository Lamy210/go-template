// Package outbox provides transactional event persistence primitives.
//
// It deliberately stops at durable storage/claim/settlement. Transport-specific
// dispatch belongs to a separate composition layer.
package outbox

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	coreprop "github.com/Lamy210/go-template/internal/core/propagation"
	"github.com/Lamy210/go-template/internal/messageid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	maxEventIDBytes   = 128
	maxSubjectBytes   = 255
	maxPayloadBytes   = 1 << 20
	maxTraceparentLen = 256
	maxTracestateLen  = 512
	maxClaimLease     = 24 * time.Hour
)

// DBTX is the minimal pgx boundary needed by transactional enqueue.
type DBTX interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

// Event is the durable, transport-neutral message intent written inside the
// same PostgreSQL transaction as application state changes.
type Event struct {
	ID      string
	Subject string
	Payload []byte
}

// ClaimedEvent is one leased outbox record ready for external dispatch.
type ClaimedEvent struct {
	ID          int64
	EventID     string
	Subject     string
	Payload     []byte
	Traceparent string
	Tracestate  string
	Attempts    int
	LockToken   string
}

// ClaimConfig bounds one dispatcher claim operation.
type ClaimConfig struct {
	BatchSize int
	Lease     time.Duration
}

// Validate enforces bounded event payload/metadata before touching PostgreSQL.
func (e Event) Validate() error {
	if len(e.ID) == 0 || len(e.ID) > maxEventIDBytes {
		return fmt.Errorf("outbox event ID must contain 1-%d bytes", maxEventIDBytes)
	}
	if err := messageid.Validate(e.ID); err != nil {
		return fmt.Errorf("outbox event ID is invalid: %w", err)
	}
	if strings.TrimSpace(e.Subject) == "" || len(e.Subject) > maxSubjectBytes {
		return fmt.Errorf("outbox subject must contain 1-%d bytes", maxSubjectBytes)
	}
	if !utf8.ValidString(e.Subject) {
		return errors.New("outbox subject must be valid UTF-8")
	}
	if strings.ContainsRune(e.Subject, '\x00') {
		return errors.New("outbox subject must not contain NUL")
	}
	if len(e.Payload) > maxPayloadBytes {
		return fmt.Errorf("outbox payload must not exceed %d bytes", maxPayloadBytes)
	}
	return nil
}

// Validate rejects unbounded or nonsensical claim behavior.
func (c ClaimConfig) Validate() error {
	if c.BatchSize <= 0 {
		return errors.New("outbox claim batch size must be positive")
	}
	if c.BatchSize > 1000 {
		return errors.New("outbox claim batch size must not exceed 1000")
	}
	if c.Lease < time.Microsecond {
		return errors.New("outbox claim lease must be at least one microsecond")
	}
	if c.Lease > maxClaimLease {
		return errors.New("outbox claim lease must not exceed 24 hours")
	}
	return nil
}

type propagationCarrier struct {
	values map[string]string
}

var _ coreprop.TextMapCarrier = (*propagationCarrier)(nil)

func newPropagationCarrier() *propagationCarrier {
	return &propagationCarrier{values: make(map[string]string)}
}

func (c *propagationCarrier) Get(key string) string {
	for existingKey, value := range c.values {
		if strings.EqualFold(existingKey, key) {
			return value
		}
	}
	return ""
}

func (c *propagationCarrier) Set(key, value string) {
	// Transactional outbox persistence intentionally keeps only W3C tracing
	// headers. Baggage can contain secrets/PII/high-cardinality data and is
	// therefore not stored by this generic template.
	if !strings.EqualFold(key, "traceparent") &&
		!strings.EqualFold(key, "tracestate") {
		return
	}
	for existingKey := range c.values {
		if strings.EqualFold(existingKey, key) {
			c.values[existingKey] = value
			return
		}
	}
	c.values[key] = value
}

func (c *propagationCarrier) Keys() []string {
	keys := make([]string, 0, len(c.values))
	for key := range c.values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// operationError preserves the underlying PostgreSQL cause for errors.Is/
// errors.As while keeping raw driver diagnostics out of ordinary logs.
type operationError struct {
	operation string
	cause     error
}

func (e *operationError) Error() string { return e.operation }
func (e *operationError) Unwrap() error { return e.cause }

func newOperationError(operation string, cause error) error {
	if cause == nil {
		return nil
	}
	return &operationError{operation: operation, cause: cause}
}

// Keep the pgx import at the storage boundary compile-checked. Enqueue callers
// commonly pass pgx.Tx directly.
var _ DBTX = (pgx.Tx)(nil)
