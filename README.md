# go-template

Production-oriented Go service template for APIs, workers, batch jobs, webhook receivers, integration services, and internal services.

The design principle is: **small enough for a small service, structured enough for a large service**.

## Current status

The repository is being bootstrapped incrementally. Core/HTTP provides typed configuration, structured logging, transport-neutral application errors, safe HTTP error translation and panic recovery, build metadata, explicit server limits/timeouts, synchronous listener bind before background workers, bounded graceful shutdown with forced close on deadline, Huma-generated OpenAPI, health probes, tests, Docker, and CI.

An optional PostgreSQL profile adds pgx/v5, sqlc, Atlas versioned migrations, bounded DB readiness and shutdown, explicit transaction boundaries, local Compose, fresh-database integration tests, and self-contained Testcontainers execution. It is disabled by default so DB-free services stay simple.

An optional NATS JetStream profile adds bounded connection/reconnect policy, explicit stream limits, message-ID deduplication, durable pull consumers, finite retry/quarantine behavior, dependency readiness with managed-stream drift detection, and graceful drain. It is also disabled by default.

An optional OpenTelemetry profile exports stable traces and metrics over OTLP/HTTP with bounded buffering, retry, export, and shutdown behavior. It is disabled by default and is deliberately non-critical to readiness.

A PostgreSQL transactional outbox supports atomic application-state + future-message persistence with lease-based multi-dispatcher claiming. An optional bounded runtime dispatcher publishes claimed rows through the messaging boundary while keeping the outbox package transport-neutral.

## Requirements

- Go 1.27.x
- Docker (optional for the application; used by local PostgreSQL/NATS and integration workflows; local Compose ports bind to 127.0.0.1 by default)
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
- `GET /openapi.json` when `HTTP_DOCS_ENABLED=true`
- `GET /docs` when `HTTP_DOCS_ENABLED=true`

## Error model

Application code uses transport-neutral errors from `internal/core/apperror`. HTTP adapters map them to a safe response containing a machine-readable `code`, public `message`, optional `details`, and `request_id`.

Raw dependency errors are not returned to clients. Platform adapters use the shared `internal/core/safeerror` wrapper so ordinary error strings contain only safe operation names while the original cause remains available to `errors.Is` / `errors.As`. Unknown errors collapse to a generic HTTP 500 response. Huma validation/framework errors are normalized into the same response contract.

See [docs/core.md](docs/core.md) for ownership and helper rules.

## Configuration

Configuration is read once at startup from environment variables. See `.env.example` for supported values.

`SERVICE_NAME` defaults to `go-service` and is attached to every structured log together with `version` and `environment`.

`DATABASE_ENABLED=false`, `NATS_ENABLED=false`, `TELEMETRY_ENABLED=false`, and `OUTBOX_DISPATCH_ENABLED=false` are the defaults. When a profile is disabled, its dependency-specific settings are intentionally ignored so stale configuration cannot break a service that does not use that capability.

Invalid configuration for an enabled capability fails fast before the server begins accepting traffic.

`HTTP_DOCS_ENABLED=true` preserves the development-friendly default Huma documentation surface. Set it to `false` when the service should not expose the built-in Docs UI, OpenAPI documents, or JSON Schema routes. The template disables Huma's response schema-link transformer regardless of this setting, so normal API responses do not gain `$schema` fields or schema `Link` headers derived from request/forwarded host metadata.

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

## Transactional outbox

Durable outbox enqueue is available as a PostgreSQL primitive independently of the background dispatcher. Enable runtime dispatch only when both PostgreSQL and NATS are enabled:

```bash
export DATABASE_ENABLED=true
export NATS_ENABLED=true
export OUTBOX_DISPATCH_ENABLED=true
```

The dispatcher claims rows with leases, publishes a bounded concurrent batch with stable `event_id` message IDs, applies finite exponential retry, and settles rows using lease tokens. Claim, publish, store operations, and shutdown all have explicit time budgets.

See [docs/outbox.md](docs/outbox.md).

## NATS JetStream profile

For local messaging integration:

```bash
make nats-up
make test-messaging
make test-outbox
make nats-down
```

The application opens NATS and creates the configured stream when missing only
when `NATS_ENABLED=true`. Existing managed stream or durable-consumer drift is rejected rather than
silently updated, so JetStream migrations remain explicit operational changes. Business consumers remain feature-owned; the template provides the bounded JetStream infrastructure rather than hardwiring a generic worker. When telemetry is also enabled, W3C Trace Context/Baggage is propagated through NATS headers and logical publish/process spans are created around messaging operations.

See [docs/messaging.md](docs/messaging.md).

## OpenTelemetry profile

Enable OTLP traces and metrics with a collector or compatible backend:

```bash
export TELEMETRY_ENABLED=true
export OTEL_EXPORTER_OTLP_ENDPOINT='http://127.0.0.1:4318'
make dev
```

The profile exports to `/v1/traces` and `/v1/metrics`, propagates W3C Trace Context/Baggage, and instruments inbound HTTP. HTTP server spans are normalized with matched chi route templates rather than raw URL paths. Collector availability is intentionally not part of `/health/ready`; telemetry failures must not evict an otherwise healthy service.

OpenTelemetry logging is not enabled in this profile. Structured application logs remain on `log/slog`; HTTP access logs include the active `trace_id` and `span_id` when telemetry is enabled. Access logs use matched chi route templates (or `<unmatched>`) rather than raw URL paths.

See [docs/telemetry.md](docs/telemetry.md).

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

GitHub Actions runs module consistency, formatting, vet, race-enabled tests, metadata-injected binary build, Docker build, hardened container runtime smoke testing (non-root, read-only root filesystem, dropped capabilities, no-new-privileges), lint, vulnerability scanning, Compose validation, sqlc generation, migration integrity, fresh PostgreSQL migration, schema drift detection, external-database integration, self-contained Testcontainers integration, real NATS JetStream integration, and combined PostgreSQL + JetStream outbox dispatch integration.

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
    propagation/           transport-neutral text-map context contract
    safeerror/             sanitized infrastructure cause wrappers
  modules/
    example/store/sqlc/    removable generated DB example
  platform/
    database/              PostgreSQL pool/readiness/transaction boundary
    httpserver/            HTTP transport
    messaging/             NATS JetStream client/stream/consumer boundary
    telemetry/             OpenTelemetry traces/metrics boundary
    outbox/                transactional outbox storage/dispatcher boundary
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
  messaging.md
  telemetry.md
  outbox.md
  development.md
```

`cmd/*` must not contain business logic. Feature code should live under `internal/modules/<feature>`, stable architecture-level primitives under `internal/core`, and infrastructure details under `internal/platform`.

## Development workflow

See [docs/development.md](docs/development.md) for branch naming, commit conventions, coding rules, and local commands. Dependency update scope and manual pins are documented in [docs/dependencies.md](docs/dependencies.md).
