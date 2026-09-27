//go:build integration

package integration

import (
	"context"
	"errors"
	"os"
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

func TestDatabaseReadinessCheck(t *testing.T) {
	pool, ctx := openTestPool(t)

	check := database.ReadinessCheck(pool, time.Second)
	if err := check(ctx); err != nil {
		t.Fatalf("readiness check: %v", err)
	}
}

func openTestPool(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("DATABASE_URL is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping postgres: %v", err)
	}

	return pool, ctx
}
