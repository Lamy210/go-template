package database

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var errTransactionCallbackPanic = errors.New("postgres transaction callback panicked")

// InTx executes fn in one PostgreSQL transaction.
//
// Transaction ownership stays at the use-case boundary. Repositories should
// accept the pgx/sqlc DBTX they are given rather than starting transactions
// implicitly.
func InTx(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) error {
	if pool == nil {
		return errors.New("postgres pool must not be nil")
	}
	if fn == nil {
		return errors.New("postgres transaction callback must not be nil")
	}

	if err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		return invokeTransactionCallback(fn, tx)
	}); err != nil {
		return newOperationError("postgres transaction", err)
	}
	return nil
}

func invokeTransactionCallback(fn func(pgx.Tx) error, tx pgx.Tx) (err error) {
	defer func() {
		if recover() != nil {
			err = errTransactionCallbackPanic
		}
	}()
	return fn(tx)
}
