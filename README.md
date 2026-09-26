# go-template

Production-oriented Go service template for APIs, workers, batch jobs, webhook receivers, integration services, and internal services.

The design principle is: **small enough for a small service, structured enough for a large service**.

## Current status

The repository is being bootstrapped incrementally. The current HTTP profile provides typed configuration, structured logging, explicit server limits/timeouts, graceful shutdown, Huma-generated OpenAPI, health probes, tests, Docker, and CI.

Database, messaging, telemetry, migrations, and other optional profiles are added separately so that the core remains removable and understandable.

## Requirements

- Go 1.27.x
- Docker (optional)

## Quick start

```bash
go mod tidy
make dev
```

The default server listens on `:8080`.

Useful endpoints:

- `GET /health/live`
- `GET /health/ready`
- `GET /openapi.json`
- `GET /docs`

## Configuration

Configuration is read once at startup from environment variables. See `.env.example` for supported values.

Invalid or unsafe configuration fails fast before the server begins accepting traffic.

## Quality gates

```bash
make test
make vet
make lint
make vuln
make build
```

GitHub Actions runs formatting, vet, race-enabled tests, build, lint, and vulnerability scanning for pull requests and `main`.

## Repository layout

```text
cmd/
  api/                    process entrypoint
internal/
  app/                    dependency composition and lifecycle
  config/                 typed environment configuration
  modules/                feature-oriented application modules
  platform/               transport/infrastructure adapters
docs/
  architecture.md
  development.md
```

`cmd/*` must not contain business logic. Feature code should live under `internal/modules/<feature>` and infrastructure details under `internal/platform`.

## Development workflow

See [docs/development.md](docs/development.md) for branch naming, commit conventions, coding rules, and local commands.
