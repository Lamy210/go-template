package database

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// InTx executes fn in one PostgreSQL transaction.
//
// Transaction ownership stays at the use-case boundary. Repositories should
// accept the pgx/sqlc DBTX they are given rather than starting transactions
// implicitly.
func InTx(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) error {
	if err := pgx.BeginFunc(ctx, pool, fn); err != nil {
		return newOperationError("postgres transaction", err)
	}
	return nil
}
