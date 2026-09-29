// Package database contains the optional PostgreSQL infrastructure profile.
package database

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Lamy210/go-template/internal/core/safeerror"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Config contains pgxpool settings supplied by the application composition root.
type Config struct {
	URL               string
	MaxConns          int32
	MinConns          int32
	MaxConnLifetime   time.Duration
	MaxConnIdleTime   time.Duration
	HealthCheckPeriod time.Duration
	ConnectTimeout    time.Duration
}

// Validate enforces the pool invariants owned by the PostgreSQL adapter.
//
// The database URL is never included in returned error text.
func (c Config) Validate() error {
	if strings.TrimSpace(c.URL) == "" {
		return errors.New("postgres URL must not be empty")
	}
	if c.MaxConns <= 0 {
		return errors.New("postgres max connections must be positive")
	}
	if c.MinConns < 0 {
		return errors.New("postgres min connections must not be negative")
	}
	if c.MinConns > c.MaxConns {
		return errors.New("postgres min connections must not exceed max connections")
	}
	if c.MaxConnLifetime <= 0 {
		return errors.New("postgres max connection lifetime must be positive")
	}
	if c.MaxConnIdleTime <= 0 {
		return errors.New("postgres max connection idle time must be positive")
	}
	if c.HealthCheckPeriod <= 0 {
		return errors.New("postgres health check period must be positive")
	}
	if c.ConnectTimeout <= 0 {
		return errors.New("postgres connect timeout must be positive")
	}
	return nil
}

// Open creates and verifies a PostgreSQL pool.
//
// Raw driver errors remain available through errors.Is/errors.As traversal but
// are not included in Error(), which prevents connection details from leaking
// through ordinary structured logging.
func Open(ctx context.Context, cfg Config) (*pgxpool.Pool, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	poolConfig, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, safeerror.Wrap("parse postgres configuration", err)
	}

	poolConfig.MaxConns = cfg.MaxConns
	poolConfig.MinConns = cfg.MinConns
	poolConfig.MaxConnLifetime = cfg.MaxConnLifetime
	poolConfig.MaxConnIdleTime = cfg.MaxConnIdleTime
	poolConfig.HealthCheckPeriod = cfg.HealthCheckPeriod
	poolConfig.ConnConfig.ConnectTimeout = cfg.ConnectTimeout

	connectCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(connectCtx, poolConfig)
	if err != nil {
		return nil, safeerror.Wrap("create postgres pool", err)
	}
	if err := pool.Ping(connectCtx); err != nil {
		pool.Close()
		return nil, safeerror.Wrap("ping postgres", err)
	}
	return pool, nil
}

// ReadinessCheck returns a bounded dependency check suitable for HTTP readiness.
func ReadinessCheck(pool *pgxpool.Pool, timeout time.Duration) func(context.Context) error {
	return func(ctx context.Context) error {
		if timeout <= 0 {
			return errors.New("postgres readiness timeout must be positive")
		}
		if pool == nil {
			return errors.New("postgres pool must not be nil")
		}

		checkCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		if err := pool.Ping(checkCtx); err != nil {
			return safeerror.Wrap("ping postgres", err)
		}
		return nil
	}
}
