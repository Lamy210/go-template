# Architecture

## Goals

This repository is a reusable Go service template. It must stay small enough for a simple service while keeping clear boundaries for larger services.

## Direction

The default architecture is a modular monolith with ports/adapters only where an external boundary or multiple implementations justify an interface.

```text
cmd/*
  -> internal/app
      -> internal/modules/*
      -> internal/platform/*
```

`cmd/*` is composition only: configuration, dependency initialization, process start, and shutdown. Business rules belong in feature-oriented packages under `internal/modules`.

## Current bootstrap

The first increment provides:

- typed environment configuration with startup validation;
- structured `log/slog` logging;
- a bounded HTTP server with explicit timeouts and request/body limits;
- graceful shutdown;
- Huma OpenAPI 3.1 generation on top of chi;
- liveness/readiness probes;
- unit/transport tests;
- Docker and CI quality gates.

Database, messaging, telemetry, and code generation are intentionally not coupled into the core bootstrap. They should be added as optional profiles so that deleting an unused profile does not break the service skeleton.
