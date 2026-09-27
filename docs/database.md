# PostgreSQL Profile

The PostgreSQL profile is optional. A service without PostgreSQL keeps
`DATABASE_ENABLED=false` and starts without a database dependency. When the
profile is disabled, PostgreSQL-specific values are not parsed or validated.

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

Configuration is validated at two boundaries: the environment-facing config
layer and the PostgreSQL adapter itself. The adapter independently rejects an
empty URL, invalid pool sizes, `MinConns > MaxConns`, and non-positive pool
timeouts/lifetimes. This keeps the infrastructure package safe when it is reused
outside the default application composition.

The database URL is treated as a secret. Database infrastructure errors retain
their underlying cause for `errors.Is` / `errors.As`, but their normal
`Error()` string contains only the safe operation name. This prevents raw
driver diagnostics or connection details from entering ordinary application
logs.

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
make migrate-diff \
  NAME=add_example_field \
  DATABASE_DEV_URL='docker://postgres/18/dev?search_path=public'
```

Atlas Community supports `migrate diff`, `apply`, and `status`, but not
`migrate down`. Projects that require automated down migrations should choose
and document a different migration policy instead of silently depending on an
unavailable command.

Application startup never runs migrations automatically.

## Schema consistency

CI does more than check that migrations execute. After applying the entire
history to a fresh PostgreSQL database, Atlas runs `schema diff` against
`sql/schema/schema.sql` and excludes only `atlas_schema_revisions`. Any
remaining SQL plan is treated as drift and fails the build.

This keeps the sqlc schema input and migration history from silently diverging.

## Transactions

`internal/platform/database.InTx` is a small boundary around pgx transaction
lifecycle. Use cases decide when multiple repository operations belong to one
transaction; repositories do not start transactions on their own.

Integration tests verify that rollback occurs and that the original callback
error remains discoverable through `errors.Is`. The helper also rejects a nil
pool or nil transaction callback instead of allowing a panic at the common
transaction boundary.

## Readiness and shutdown

When the profile is enabled:

1. startup creates and pings the pool within `DATABASE_CONNECT_TIMEOUT`;
2. `/health/ready` performs a bounded ping using
   `DATABASE_HEALTH_TIMEOUT`;
3. HTTP drains first on shutdown;
4. the pool closes as application composition unwinds.

A database error can make readiness fail, but its raw dependency text is not
returned to HTTP clients.

## Testcontainers

Integration tests live in their own Go module under `test/integration` so the
main service dependency graph does not inherit Testcontainers and its Docker
client dependencies.

Running:

```bash
make test-integration
```

without `DATABASE_URL` starts a pinned PostgreSQL 18.6 container using
Testcontainers for Go, applies the repository migrations as ordered init
scripts, runs the same generated-query / transaction / readiness tests, and
removes the container afterward.

To test against an already-running database instead:

```bash
make test-integration-external DATABASE_URL='postgres://...'
```

CI keeps both paths: the `database` job validates Atlas against a fresh service
database, while the `testcontainers` job proves that integration tests are
self-contained and portable.
