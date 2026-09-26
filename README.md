# go-template

Production-oriented Go service template for APIs, workers, batch jobs, webhook receivers, integration services, and internal services.

The design principle is: **small enough for a small service, structured enough for a large service**.

## Current status

The repository is being bootstrapped incrementally. Core/HTTP provides typed configuration, structured logging, transport-neutral application errors, safe HTTP error translation, build metadata, explicit server limits/timeouts, graceful shutdown, Huma-generated OpenAPI, health probes, tests, Docker, and CI.

An optional PostgreSQL profile adds pgx/v5, sqlc, Atlas versioned migrations, bounded DB readiness, explicit transaction boundaries, local Compose, fresh-database integration tests, and self-contained Testcontainers execution. It is disabled by default so DB-free services stay simple.

## Requirements

- Go 1.27.x
- Docker (optional for the application; used by local PostgreSQL and CI database workflows)
- sqlc / Atlas CLI only when working on the PostgreSQL profile locally

## Quick start

```bash
go mod tidy
make dev
```

The default server listens on `:8080`.

Useful endpoints:

- `GET /health/live`
- `GET /health/ready`
- `GET /version`
- `GET /openapi.json`
- `GET /docs`

## Error model

Application code uses transport-neutral errors from `internal/core/apperror`. HTTP adapters map them to a safe response containing a machine-readable `code`, public `message`, optional `details`, and `request_id`.

Raw dependency errors are not returned to clients. Unknown errors collapse to a generic HTTP 500 response. Huma validation/framework errors are normalized into the same response contract.

See [docs/core.md](docs/core.md) for ownership and helper rules.

## Configuration

Configuration is read once at startup from environment variables. See `.env.example` for supported values.

`SERVICE_NAME` defaults to `go-service` and is attached to every structured log together with `version` and `environment`.

`DATABASE_ENABLED=false` is the default. When disabled, PostgreSQL-specific settings are intentionally ignored so a stale DB setting cannot break a DB-free service.

Invalid configuration for an enabled capability fails fast before the server begins accepting traffic.

## PostgreSQL profile

For local PostgreSQL:

```bash
make db-up
export DATABASE_URL='postgres://app:app@localhost:5432/app?sslmode=disable'
make migrate-up
make test-integration-external DATABASE_URL="$DATABASE_URL"
```

Application startup never runs migrations automatically. Generated sqlc code and `migrations/atlas.sum` are committed and checked for drift in CI. `make test-integration` can run without a pre-existing database by starting PostgreSQL through Testcontainers.

See [docs/database.md](docs/database.md).

## Build metadata

Binary metadata is injected at link time through `internal/buildinfo` and exposed through `GET /version`.

```bash
make build \
  VERSION=v1.2.3 \
  COMMIT="$(git rev-parse HEAD)" \
  BUILD_DATE=2026-09-27T00:00:00Z
```

The Docker build accepts the same values via `VERSION`, `COMMIT`, and `BUILD_DATE` build arguments. Defaults are deterministic placeholders (`dev` / `unknown`) rather than a generated wall-clock timestamp.

## Quality gates

```bash
make test
make vet
make lint
make vuln
make build
```

GitHub Actions runs module consistency, formatting, vet, race-enabled tests, metadata-injected binary build, Docker build, lint, vulnerability scanning, sqlc generation, migration integrity, fresh PostgreSQL migration, schema drift detection, and database integration tests.

## Repository layout

```text
cmd/
  api/                    process entrypoint
internal/
  app/                    dependency composition and lifecycle
  buildinfo/              link-time build metadata
  config/                 typed environment configuration
  core/                   stable transport-neutral shared contracts
    apperror/              application error semantics
  modules/
    example/store/sqlc/    removable generated DB example
  platform/
    database/              PostgreSQL pool/readiness/transaction boundary
    httpserver/            HTTP transport
migrations/                versioned Atlas migrations + atlas.sum
sql/
  schema/                  desired SQL schema
  queries/                 named sqlc queries
test/
  integration/             separate Go module; external DB or Testcontainers
docs/
  architecture.md
  core.md
  database.md
  development.md
```

`cmd/*` must not contain business logic. Feature code should live under `internal/modules/<feature>`, stable architecture-level primitives under `internal/core`, and infrastructure details under `internal/platform`.

## Development workflow

See [docs/development.md](docs/development.md) for branch naming, commit conventions, coding rules, and local commands. Dependency update scope and manual pins are documented in [docs/dependencies.md](docs/dependencies.md).
