// Package database contains the optional PostgreSQL infrastructure profile.
package database

import (
	"context"
	"time"

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

// Open creates and verifies a PostgreSQL pool.
//
// Raw driver errors remain available through errors.Is/errors.As traversal but
// are not included in Error(), which prevents connection details from leaking
// through ordinary structured logging.
func Open(ctx context.Context, cfg Config) (*pgxpool.Pool, error) {
	poolConfig, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, newOperationError("parse postgres configuration", err)
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
		return nil, newOperationError("create postgres pool", err)
	}
	if err := pool.Ping(connectCtx); err != nil {
		pool.Close()
		return nil, newOperationError("ping postgres", err)
	}
	return pool, nil
}

// ReadinessCheck returns a bounded dependency check suitable for HTTP readiness.
func ReadinessCheck(pool *pgxpool.Pool, timeout time.Duration) func(context.Context) error {
	return func(ctx context.Context) error {
		checkCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		if err := pool.Ping(checkCtx); err != nil {
			return newOperationError("ping postgres", err)
		}
		return nil
	}
}
