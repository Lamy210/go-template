package integration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

const postgresImage = "postgres:18.6-alpine@sha256:77f585114c32fbca283dc835b0596f4e52b51b4c6662d7810b2f4084f60a1873"

var testDatabaseURL string

func TestMain(m *testing.M) {
	testDatabaseURL = os.Getenv("DATABASE_URL")

	var terminate func() error
	if testDatabaseURL == "" {
		var err error
		testDatabaseURL, terminate, err = startPostgresContainer()
		if err != nil {
			fmt.Fprintf(os.Stderr, "start test postgres: %v\n", err)
			os.Exit(1)
		}
	}

	code := m.Run()
	if terminate != nil {
		if err := terminate(); err != nil {
			fmt.Fprintf(os.Stderr, "terminate test postgres: %v\n", err)
			if code == 0 {
				code = 1
			}
		}
	}

	os.Exit(code)
}

func startPostgresContainer() (string, func() error, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	migrations, err := filepath.Glob("../../migrations/*.sql")
	if err != nil {
		return "", nil, fmt.Errorf("list migrations: %w", err)
	}
	if len(migrations) == 0 {
		return "", nil, fmt.Errorf("no migrations found")
	}
	sort.Strings(migrations)

	container, err := postgres.Run(
		ctx,
		postgresImage,
		postgres.WithDatabase("app"),
		postgres.WithUsername("app"),
		postgres.WithPassword("app"),
		postgres.WithOrderedInitScripts(migrations...),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(time.Minute),
		),
	)
	if err != nil {
		return "", nil, fmt.Errorf("run postgres container: %w", err)
	}

	terminate := func() error {
		return testcontainers.TerminateContainer(container)
	}

	databaseURL, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = terminate()
		return "", nil, fmt.Errorf("get postgres connection string: %w", err)
	}

	return databaseURL, terminate, nil
}
