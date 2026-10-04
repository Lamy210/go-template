package outbox

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/Lamy210/go-template/internal/outboxbudget"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var errNilDBTX = errors.New("outbox DBTX must not be nil")

const claimSQL = `
WITH candidates AS (
    SELECT id
    FROM outbox_events
    WHERE published_at IS NULL
      AND failed_at IS NULL
      AND available_at <= CURRENT_TIMESTAMP
      AND (locked_until IS NULL OR locked_until <= CURRENT_TIMESTAMP)
    ORDER BY available_at, id
    FOR UPDATE SKIP LOCKED
    LIMIT $1
)
UPDATE outbox_events AS event
SET attempts = event.attempts + 1,
    locked_until = CURRENT_TIMESTAMP + $2::interval,
    lock_token = $3
FROM candidates
WHERE event.id = candidates.id
RETURNING
    event.id,
    event.event_id,
    event.subject,
    event.payload,
    event.traceparent,
    event.tracestate,
    event.attempts,
    event.lock_token
`

const markPublishedSQL = `
UPDATE outbox_events
SET published_at = CURRENT_TIMESTAMP,
    locked_until = NULL,
    lock_token = NULL
WHERE id = $1
  AND lock_token = $2
  AND locked_until > CURRENT_TIMESTAMP
  AND published_at IS NULL
  AND failed_at IS NULL
`

const retrySQL = `
UPDATE outbox_events
SET available_at = CURRENT_TIMESTAMP + $3::interval,
    locked_until = NULL,
    lock_token = NULL
WHERE id = $1
  AND lock_token = $2
  AND locked_until > CURRENT_TIMESTAMP
  AND published_at IS NULL
  AND failed_at IS NULL
`

const failSQL = `
UPDATE outbox_events
SET failed_at = CURRENT_TIMESTAMP,
    locked_until = NULL,
    lock_token = NULL
WHERE id = $1
  AND lock_token = $2
  AND locked_until > CURRENT_TIMESTAMP
  AND published_at IS NULL
  AND failed_at IS NULL
`

// Store owns lease-based outbox dispatch state transitions.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore validates and wraps an existing PostgreSQL pool.
func NewStore(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, errors.New("outbox postgres pool must not be nil")
	}
	return &Store{pool: pool}, nil
}

// Claim leases up to BatchSize available events without holding a database
// transaction open during external publish calls.
func (s *Store) Claim(ctx context.Context, cfg ClaimConfig) ([]ClaimedEvent, error) {
	if s == nil || s.pool == nil {
		return nil, errors.New("outbox store must not be nil")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	token := rand.Text()

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, newOperationError("begin outbox claim transaction", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	rows, err := tx.Query(ctx, claimSQL, cfg.BatchSize, durationInterval(cfg.Lease), token)
	if err != nil {
		return nil, newOperationError("claim outbox events", err)
	}
	defer rows.Close()

	claimed := make([]ClaimedEvent, 0, cfg.BatchSize)
	for rows.Next() {
		var event ClaimedEvent
		if err := rows.Scan(
			&event.ID,
			&event.EventID,
			&event.Subject,
			&event.Payload,
			&event.Traceparent,
			&event.Tracestate,
			&event.Attempts,
			&event.LockToken,
		); err != nil {
			return nil, newOperationError("scan claimed outbox event", err)
		}
		claimed = append(claimed, event)
	}
	if err := rows.Err(); err != nil {
		return nil, newOperationError("iterate claimed outbox events", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, newOperationError("commit outbox claim transaction", err)
	}
	return claimed, nil
}

// MarkPublished settles a claimed record after successful external publish.
func (s *Store) MarkPublished(ctx context.Context, event ClaimedEvent) error {
	return s.transition(ctx, markPublishedSQL, event)
}

// Retry releases a claim and schedules another attempt after delay.
func (s *Store) Retry(ctx context.Context, event ClaimedEvent, delay time.Duration) error {
	if delay < time.Microsecond {
		return errors.New("outbox retry delay must be at least one microsecond")
	}
	if !outboxbudget.FitsPostgresIntervalPrecision(delay) {
		return errors.New("outbox retry delay must use whole microseconds")
	}
	if delay > maxClaimLease {
		return errors.New("outbox retry delay must not exceed 24 hours")
	}
	return s.transition(
		ctx,
		retrySQL,
		event,
		durationInterval(delay),
	)
}

// MarkFailed permanently stops automatic dispatch for a claimed event.
func (s *Store) MarkFailed(ctx context.Context, event ClaimedEvent) error {
	return s.transition(ctx, failSQL, event)
}

func validateClaimIdentity(event ClaimedEvent) error {
	if event.ID <= 0 {
		return errors.New("outbox claimed event ID must be positive")
	}
	if event.LockToken == "" {
		return errors.New("outbox claimed event lock token must not be empty")
	}
	return nil
}

func (s *Store) transition(
	ctx context.Context,
	query string,
	event ClaimedEvent,
	extraArgs ...any,
) error {
	if s == nil || s.pool == nil {
		return errors.New("outbox store must not be nil")
	}
	if err := validateClaimIdentity(event); err != nil {
		return err
	}

	args := make([]any, 0, 2+len(extraArgs))
	args = append(args, event.ID, event.LockToken)
	args = append(args, extraArgs...)

	tag, err := s.pool.Exec(ctx, query, args...)
	if err != nil {
		return newOperationError("settle outbox event", err)
	}
	if tag.RowsAffected() != 1 {
		return newOperationError(
			"settle outbox event",
			errors.New("outbox claim no longer owned"),
		)
	}
	return nil
}

func durationInterval(value time.Duration) string {
	// PostgreSQL accepts a textual interval parameter. Format the value as an
	// exact microsecond count to avoid locale/unit ambiguity and floating point.
	micros := value / time.Microsecond
	return fmt.Sprintf("%d microseconds", micros)
}
