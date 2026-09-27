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

Health endpoints are HTTP transport concerns, so their Huma registrations live under `internal/platform/httpserver` rather than pretending to be a business module.

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

## Current bootstrap

The first increment provides:

- typed environment configuration with startup validation;
- structured `log/slog` logging;
- transport-neutral application error semantics;
- safe HTTP error translation with stable codes and request IDs;
- a bounded HTTP server with explicit timeouts and request/body limits;
- graceful shutdown;
- Huma OpenAPI 3.1 generation on top of chi;
- liveness/readiness probes;
- unit/transport tests;
- Docker and CI quality gates.

Database, messaging, telemetry, and code generation are intentionally not coupled into the core bootstrap. They should be added as optional profiles so that deleting an unused profile does not break the service skeleton.
