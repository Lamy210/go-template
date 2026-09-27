# Development

## Branch policy

`main` is the releasable branch. Do not develop directly on `main`.

Use short-lived branches named by intent:

- `feat/<topic>` for features;
- `fix/<topic>` for bug fixes;
- `chore/<topic>` for maintenance;
- `docs/<topic>` for documentation-only work.

Open a pull request into `main`. Keep a branch focused on one coherent change and prefer squash merge after required checks are green.

## Commit messages

Use Conventional Commit-style subjects where practical, for example:

```text
feat(http): add bounded server bootstrap
fix(config): reject invalid timeout values
```

## Coding rules

- Prefer simple, explicit Go over framework-heavy abstractions.
- Use early returns and wrap errors with useful context.
- Keep package APIs small and package names meaningful.
- Do not create catch-all `utils`, `common`, `helpers`, or `misc` packages.
- Keep one-off helpers unexported beside their owner.
- Promote code into `internal/core` only when it is transport-neutral, stable, and genuinely shared.
- Do not put business logic in `cmd/*` or HTTP handlers.
- Add interfaces at architectural boundaries, not solely to make mocking easier.
- Pass `context.Context` through I/O paths; do not use it as a dependency container.
- Avoid hidden globals for database handles, loggers, configuration, or error-model mutation.
- Bound concurrency, retries, request sizes, and timeouts.
- Never log secrets, passwords, access tokens, authorization headers, raw URL paths by default, or raw dependency errors that may contain sensitive values.
- Use `errors.Is` / `errors.AsType` for wrapped-error inspection rather than branching on error strings.

See [core.md](core.md) for core, common-definition, helper, and error ownership rules.

## Local commands

```bash
make dev
make test
make vet
make lint
make vuln
make build

# PostgreSQL profile
make db-up
make test-integration
make db-down

# NATS JetStream profile
make nats-up
make test-messaging
make nats-down

# OpenTelemetry profile (requires an OTLP/HTTP collector/backend)
TELEMETRY_ENABLED=true OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318 make dev
```

Copy `.env.example` values into your process environment as needed. A `.env` file is intentionally ignored by Git.
