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
- Do not create catch-all `utils`, `common`, or `helpers` packages.
- Do not put business logic in `cmd/*` or HTTP handlers.
- Add interfaces at architectural boundaries, not solely to make mocking easier.
- Pass `context.Context` through I/O paths; do not use it as a dependency container.
- Avoid hidden globals for database handles, loggers, or configuration.
- Bound concurrency, retries, request sizes, and timeouts.
- Never log secrets, passwords, access tokens, or authorization headers.

## Local commands

```bash
make dev
make test
make vet
make lint
make vuln
make build
```

Copy `.env.example` values into your process environment as needed. A `.env` file is intentionally ignored by Git.
