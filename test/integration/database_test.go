package integration

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	exampledb "github.com/Lamy210/go-template/internal/modules/example/store/sqlc"
	"github.com/Lamy210/go-template/internal/platform/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestGeneratedQueriesAgainstMigratedPostgres(t *testing.T) {
	pool, ctx := openTestPool(t)
	queries := exampledb.New(pool)

	created, err := queries.CreateExampleItem(ctx, "integration")
	if err != nil {
		t.Fatalf("create example item: %v", err)
	}

	got, err := queries.GetExampleItem(ctx, created.ID)
	if err != nil {
		t.Fatalf("get example item: %v", err)
	}
	if got.Name != "integration" {
		t.Fatalf("Name = %q, want integration", got.Name)
	}
}

func TestTransactionRollbackPreservesCause(t *testing.T) {
	pool, ctx := openTestPool(t)
	queries := exampledb.New(pool)

	sentinel := errors.New("rollback requested by test")
	var createdID int64
	err := database.InTx(ctx, pool, func(tx pgx.Tx) error {
		created, err := queries.WithTx(tx).CreateExampleItem(ctx, "rolled-back")
		if err != nil {
			return err
		}
		createdID = created.ID
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("transaction error = %v, want wrapped sentinel", err)
	}

	_, err = queries.GetExampleItem(ctx, createdID)
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("GetExampleItem() error = %v, want pgx.ErrNoRows", err)
	}
}

func TestTransactionCallbackPanicRollsBack(t *testing.T) {
	pool, ctx := openTestPool(t)
	queries := exampledb.New(pool)

	const sensitive = "sensitive transaction panic"
	var createdID int64
	err := database.InTx(ctx, pool, func(tx pgx.Tx) error {
		created, err := queries.WithTx(tx).CreateExampleItem(ctx, "panic-rolled-back")
		if err != nil {
			return err
		}
		createdID = created.ID
		panic(sensitive)
	})
	if err == nil {
		t.Fatal("database.InTx() error = nil after callback panic")
	}
	if got := err.Error(); got != "postgres transaction" {
		t.Fatalf("database.InTx() error = %q, want sanitized operation", got)
	}
	if strings.Contains(err.Error(), sensitive) {
		t.Fatalf("database.InTx() exposed panic value: %q", err.Error())
	}

	_, err = queries.GetExampleItem(ctx, createdID)
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("GetExampleItem() error = %v, want pgx.ErrNoRows", err)
	}
}

func TestOutboxTextConstraintsUseByteLength(t *testing.T) {
	pool, ctx := openTestPool(t)

	tests := []struct {
		name  string
		query string
		args  []any
	}{
		{
			name: "event ID",
			query: `INSERT INTO outbox_events (event_id, subject, payload)
				VALUES ($1, 'events.test', ''::bytea)`,
			args: []any{strings.Repeat("雪", 43)}, // 129 UTF-8 bytes, 43 characters.
		},
		{
			name: "subject",
			query: `INSERT INTO outbox_events (event_id, subject, payload)
				VALUES ('byte-subject', $1, ''::bytea)`,
			args: []any{strings.Repeat("雪", 86)}, // 258 UTF-8 bytes, 86 characters.
		},
		{
			name: "traceparent",
			query: `INSERT INTO outbox_events (event_id, subject, payload, traceparent)
				VALUES ('byte-traceparent', 'events.test', ''::bytea, $1)`,
			args: []any{strings.Repeat("雪", 86)}, // 258 UTF-8 bytes, 86 characters.
		},
		{
			name: "tracestate",
			query: `INSERT INTO outbox_events (event_id, subject, payload, tracestate)
				VALUES ('byte-tracestate', 'events.test', ''::bytea, $1)`,
			args: []any{strings.Repeat("雪", 171)}, // 513 UTF-8 bytes, 171 characters.
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, tt.query, tt.args...); err == nil {
				t.Fatalf("insert with oversized %s succeeded", tt.name)
			}
		})
	}
}

func TestOutboxEventIDConstraintMatchesCanonicalText(t *testing.T) {
	pool, ctx := openTestPool(t)

	for _, eventID := range []string{
		" event-1",
		"event-1 ",
		"\tevent-1",
		"event-1\t",
		"event\n1",
		"event\r1",
	} {
		if _, err := pool.Exec(
			ctx,
			`INSERT INTO outbox_events (event_id, subject, payload)
			 VALUES ($1, 'events.test', ''::bytea)`,
			eventID,
		); err == nil {
			t.Fatalf("insert with non-canonical event ID %q succeeded", eventID)
		}
	}

	const validInternalTab = "event\t1"
	if _, err := pool.Exec(
		ctx,
		`INSERT INTO outbox_events (event_id, subject, payload)
		 VALUES ($1, 'events.test', ''::bytea)`,
		validInternalTab,
	); err != nil {
		t.Fatalf("insert with canonical internal TAB event ID: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM outbox_events WHERE event_id = $1", validInternalTab)
	})
}

func TestDatabaseCloseHonorsDeadlineWithAcquiredConnection(t *testing.T) {
	pool, ctx := openTestPool(t)

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire postgres connection: %v", err)
	}

	closeCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	err = database.Close(closeCtx, pool)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		conn.Release()
		t.Fatalf("database.Close() error = %v, want deadline exceeded", err)
	}

	conn.Release()

	deadline := time.Now().Add(time.Second)
	for pool.Stat().TotalConns() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := pool.Stat().TotalConns(); got != 0 {
		t.Fatalf("pool total connections after release = %d, want 0", got)
	}
}

func TestDatabaseReadinessCheck(t *testing.T) {
	pool, ctx := openTestPool(t)

	check := database.ReadinessCheck(pool, time.Second)
	if err := check(ctx); err != nil {
		t.Fatalf("readiness check: %v", err)
	}
}

func openTestPool(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()

	if testDatabaseURL == "" {
		t.Fatal("test database URL is empty")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	pool, err := pgxpool.New(ctx, testDatabaseURL)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping postgres: %v", err)
	}

	return pool, ctx
}
