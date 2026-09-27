package database

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestConfigValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{
			name: "empty URL",
			mutate: func(cfg *Config) {
				cfg.URL = " "
			},
		},
		{
			name: "non-positive max connections",
			mutate: func(cfg *Config) {
				cfg.MaxConns = 0
			},
		},
		{
			name: "negative min connections",
			mutate: func(cfg *Config) {
				cfg.MinConns = -1
			},
		},
		{
			name: "min exceeds max",
			mutate: func(cfg *Config) {
				cfg.MinConns = cfg.MaxConns + 1
			},
		},
		{
			name: "non-positive max lifetime",
			mutate: func(cfg *Config) {
				cfg.MaxConnLifetime = 0
			},
		},
		{
			name: "non-positive max idle time",
			mutate: func(cfg *Config) {
				cfg.MaxConnIdleTime = 0
			},
		},
		{
			name: "non-positive health period",
			mutate: func(cfg *Config) {
				cfg.HealthCheckPeriod = 0
			},
		},
		{
			name: "non-positive connect timeout",
			mutate: func(cfg *Config) {
				cfg.ConnectTimeout = 0
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := validDatabaseConfig()
			tt.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("Validate() error = nil, want error")
			}
		})
	}
}

func TestConfigValidationDoesNotExposeURL(t *testing.T) {
	t.Parallel()

	cfg := validDatabaseConfig()
	cfg.URL = "postgres://secret-user:secret-password@example.invalid/app"
	cfg.MaxConns = 0

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want error")
	}
	if strings.Contains(err.Error(), "secret-user") ||
		strings.Contains(err.Error(), "secret-password") ||
		strings.Contains(err.Error(), "example.invalid") {
		t.Fatalf("Validate() exposed database URL: %q", err.Error())
	}
}

func TestOpenRejectsInvalidConfigBeforeDriverParsing(t *testing.T) {
	t.Parallel()

	cfg := validDatabaseConfig()
	cfg.URL = "postgres://secret-user:secret-password@example.invalid/app"
	cfg.MinConns = cfg.MaxConns + 1

	_, err := Open(context.Background(), cfg)
	if err == nil {
		t.Fatal("Open() error = nil, want validation error")
	}
	if strings.Contains(err.Error(), "secret-password") {
		t.Fatalf("Open() exposed database URL: %q", err.Error())
	}
}

func TestReadinessCheckRejectsInvalidInputs(t *testing.T) {
	t.Parallel()

	if err := ReadinessCheck(nil, 0)(context.Background()); err == nil {
		t.Fatal("ReadinessCheck() timeout error = nil")
	}
	if err := ReadinessCheck(nil, time.Second)(context.Background()); err == nil {
		t.Fatal("ReadinessCheck() nil pool error = nil")
	}
}

func TestInTxRejectsInvalidInputs(t *testing.T) {
	t.Parallel()

	if err := InTx(context.Background(), nil, func(pgx.Tx) error { return nil }); err == nil {
		t.Fatal("InTx() nil pool error = nil")
	}

	pool := &pgxpool.Pool{}
	if err := InTx(context.Background(), pool, nil); err == nil {
		t.Fatal("InTx() nil callback error = nil")
	}
}

func validDatabaseConfig() Config {
	return Config{
		URL:               "postgres://app:app@127.0.0.1:5432/app?sslmode=disable",
		MaxConns:          10,
		MinConns:          1,
		MaxConnLifetime:   30 * time.Minute,
		MaxConnIdleTime:   5 * time.Minute,
		HealthCheckPeriod: time.Minute,
		ConnectTimeout:    5 * time.Second,
	}
}
