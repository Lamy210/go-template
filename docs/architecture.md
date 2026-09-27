# Architecture

## Goals

This repository is a reusable Go service template. It must stay small enough for a simple service while keeping clear boundaries for larger services.

## Direction

The default architecture is a modular monolith with ports/adapters only where an external boundary or multiple implementations justify an interface.

```text
cmd/*
  -> internal/app
      -> internal/modules/*
      -> internal/core/*
      -> internal/platform/*
```

`cmd/*` is composition only: configuration, dependency initialization, process start, and shutdown. Business rules belong in feature-oriented packages under `internal/modules`.

`internal/core` contains only transport-neutral, architecture-level contracts that are genuinely shared. It must not become a `common` or `utils` dumping ground. See [core.md](core.md).

Platform packages own protocol and infrastructure translation. For example, application error kinds live in `internal/core/apperror`, while HTTP status codes and the JSON error response live in `internal/platform/httpserver`.

Health and version endpoints are HTTP transport concerns, so their Huma registrations live under `internal/platform/httpserver` rather than pretending to be business modules.

## Dependency direction

```text
feature/domain
     ^
application
     ^
platform adapters

core <- feature/application/platform
```

Core may depend on the Go standard library only. Core must not import Huma, chi, PostgreSQL, NATS, or feature packages.

## Profiles

The repository keeps optional capabilities at explicit infrastructure boundaries:

- Core/HTTP: config, logging, error semantics, lifecycle, Huma/chi transport.
- PostgreSQL: `internal/platform/database`, sqlc inputs/generated example store, migrations, and integration tests.
- Future messaging/telemetry profiles should follow the same rule.

A profile must be removable without forcing unrelated application code to understand it. PostgreSQL therefore defaults to disabled, and the example sqlc package is not imported by the running application.

## PostgreSQL boundaries

The application composition root owns the pgx pool lifecycle. The database adapter owns pool configuration and bounded readiness checks. Use cases own transaction boundaries via `database.InTx`; repositories do not silently begin transactions.

The desired SQL schema and versioned migration history are both checked in CI:

1. sqlc regenerates from `sql/schema` + `sql/queries`;
2. Atlas verifies migration checksums;
3. migrations are applied to a fresh PostgreSQL instance;
4. Atlas compares the migrated database with the desired SQL schema while excluding its revisions table;
5. integration tests execute generated queries and transaction behavior.

See [database.md](database.md).

## Current bootstrap

The current increments provide:

- typed environment configuration with startup validation;
- structured `log/slog` logging;
- transport-neutral application error semantics;
- safe HTTP error translation with stable codes and request IDs;
- build metadata and a version endpoint;
- a bounded HTTP server with explicit timeouts and request/body limits;
- graceful shutdown;
- Huma OpenAPI 3.1 generation on top of chi;
- liveness/readiness probes;
- optional PostgreSQL with pgx/sqlc/Atlas;
- unit, transport, and database integration tests;
- Docker and CI quality gates.

Messaging and telemetry remain uncoupled optional profiles.
