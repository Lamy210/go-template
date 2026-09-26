//go:build integration

package integration

import (
	"context"
	"os"
	"testing"
	"time"

	exampledb "github.com/Lamy210/go-template/internal/modules/example/store/sqlc"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestGeneratedQueriesAgainstMigratedPostgres(t *testing.T) {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("DATABASE_URL is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping postgres: %v", err)
	}

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
