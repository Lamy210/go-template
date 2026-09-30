package database

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
)

type closer interface {
	Close()
}

var errPostgresClosePanic = errors.New("postgres close panicked")

// Close starts PostgreSQL pool shutdown and waits until all pool resources are
// released or ctx expires.
//
// pgxpool.Close has no context-aware variant and can wait for acquired
// connections to be returned. If ctx expires, the underlying Close continues
// in its goroutine so a later Release can still finish resource destruction,
// while the process lifecycle is no longer blocked indefinitely.
//
// A panic from the underlying closer is contained inside the shutdown
// goroutine and returned as a sanitized operation error instead of terminating
// the process.
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

	result := make(chan error, 1)
	go func() {
		result <- invokeClose(resource)
	}()

	select {
	case err := <-result:
		return newOperationError("close postgres pool", err)
	case <-ctx.Done():
		return newOperationError("close postgres pool", ctx.Err())
	}
}

func invokeClose(resource closer) (err error) {
	defer func() {
		if recover() != nil {
			err = errPostgresClosePanic
		}
	}()
	resource.Close()
	return nil
}
