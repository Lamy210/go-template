# PostgreSQL Profile

The PostgreSQL profile is optional. A service without PostgreSQL keeps
`DATABASE_ENABLED=false` and starts without a database dependency.

## Stack

- PostgreSQL 18.x for local and CI examples
- pgx/v5 for runtime access
- sqlc for type-safe query generation
- Atlas Community Edition for versioned migrations and `atlas.sum` integrity

The example schema/query under `sql/` and generated package under
`internal/modules/example/store/sqlc` are removable sample code. Application
code does not depend on that example package.

## Runtime configuration

Set:

```text
DATABASE_ENABLED=true
DATABASE_URL=postgres://...
```

Pool size, connection lifetime, health period, connect timeout, and readiness
timeout are separately configurable in `.env.example`.

The database URL is treated as a secret: startup and readiness errors never
append the URL to their own messages, and the application does not log it.

## Local PostgreSQL

```bash
make db-up
export DATABASE_URL='postgres://app:app@localhost:5432/app?sslmode=disable'
make migrate-up
```

The application can still be run directly with `go run`; Docker is not
required for the application process itself.

## sqlc

The repository pins sqlc v1.31.1 in CI. Local development expects a compatible
`sqlc` binary on PATH.

```bash
make generate
make generate-check
```

Generated code is committed. CI regenerates it and fails when the committed
output is stale.

SQL remains source code:

- queries are named;
- `SELECT *` is avoided;
- transaction boundaries are not hidden inside repositories;
- request contexts are passed into generated query methods.

## Migrations

Atlas Community Edition is the default migration tool. The repository uses a
forward-only versioned history and commits `migrations/atlas.sum`.

```bash
make migrate-hash
make migrate-status DATABASE_URL='postgres://...'
make migrate-up DATABASE_URL='postgres://...'
```

To generate a migration from the desired SQL schema:

```bash
make migrate-diff   NAME=add_example_field   DATABASE_DEV_URL='docker://postgres/18/dev?search_path=public'
```

Atlas Community supports `migrate diff`, `apply`, and `status`, but not
`migrate down`. Projects that require automated down migrations should choose
and document a different migration policy instead of silently depending on an
unavailable command.

Application startup never runs migrations automatically.

## Transactions

`internal/platform/database.InTx` is a small boundary around pgx transaction
lifecycle. Use cases decide when multiple repository operations belong to one
transaction; repositories do not start transactions on their own.

## Readiness and shutdown

When the profile is enabled:

1. startup creates and pings the pool within `DATABASE_CONNECT_TIMEOUT`;
2. `/health/ready` performs a bounded ping using
   `DATABASE_HEALTH_TIMEOUT`;
3. HTTP drains first on shutdown;
4. the pool closes as application composition unwinds.

A database error can make readiness fail, but its raw dependency text is not
returned to HTTP clients.
