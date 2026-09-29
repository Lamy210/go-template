package database

import (
	"context"
	"errors"

	"github.com/Lamy210/go-template/internal/core/safeerror"
	"github.com/jackc/pgx/v5/pgxpool"
)

type closer interface {
	Close()
}

// Close starts PostgreSQL pool shutdown and waits until all pool resources are
// released or ctx expires.
//
// pgxpool.Close has no context-aware variant and can wait for acquired
// connections to be returned. If ctx expires, the underlying Close continues
// in its goroutine so a later Release can still finish resource destruction,
// while the process lifecycle is no longer blocked indefinitely.
func Close(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return errors.New("postgres pool must not be nil")
	}
	return closeWithContext(ctx, pool)
}

func closeWithContext(ctx context.Context, resource closer) error {
	if resource == nil {
		return errors.New("postgres closer must not be nil")
	}

	done := make(chan struct{})
	go func() {
		resource.Close()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return safeerror.Wrap("close postgres pool", ctx.Err())
	}
}
