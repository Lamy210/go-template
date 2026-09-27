//go:build integration

package integration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	exampledb "github.com/Lamy210/go-template/internal/modules/example/store/sqlc"
	"github.com/Lamy210/go-template/internal/platform/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

const postgresImage = "postgres:18.6-alpine@sha256:77f585114c32fbca283dc835b0596f4e52b51b4c6662d7810b2f4084f60a1873"

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

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = startPostgresContainer(t, ctx)
	}

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

func startPostgresContainer(t *testing.T, ctx context.Context) string {
	t.Helper()

	container, err := tcpostgres.Run(
		ctx,
		postgresImage,
		tcpostgres.WithDatabase("app"),
		tcpostgres.WithUsername("app"),
		tcpostgres.WithPassword("app"),
		tcpostgres.WithInitScripts(migrationPath(t)),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("start postgres testcontainer: %v", err)
	}
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			t.Errorf("terminate postgres testcontainer: %v", err)
		}
	})

	databaseURL, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("postgres connection string: %v", err)
	}
	return databaseURL
}

func migrationPath(t *testing.T) string {
	t.Helper()

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve integration test file path")
	}
	return filepath.Join(
		filepath.Dir(currentFile),
		"..",
		"..",
		"migrations",
		"20260927000100_create_example_items.sql",
	)
}
