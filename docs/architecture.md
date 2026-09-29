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

The HTTP adapter also owns its transport-level configuration contract (listener
address, request timeouts, header/body limits, and the Huma documentation
exposure policy). `internal/app` maps the
environment-facing process config into that adapter config explicitly. The
composition root binds the configured TCP listener synchronously before starting
background workers, so a bind failure cannot occur after an outbox dispatcher or
another worker has already produced external side effects. Process shutdown
budgeting remains at the composition root because it coordinates HTTP, messaging,
telemetry, and database teardown rather than configuring `net/http.Server`
itself.

The built-in Huma documentation surface is explicitly controllable. When it is
disabled, the adapter leaves Docs UI, OpenAPI, and schema routes unregistered.

The adapter also removes Huma v2.39.1's default schema-link CreateHook regardless
of documentation exposure. The hook mutates ordinary response bodies with a
`$schema` field and derives its absolute URL from request/forwarded host
metadata. This template does not define a trusted-proxy boundary, so response
contracts remain independent of `X-Forwarded-Host` / `Forwarded`; schemas are
available only through the explicit documentation routes when enabled.

HTTP panic containment is also transport-owned. The adapter returns the same
generic `internal_error` contract when a response has not started, logs only
sanitized request metadata, and aborts already-started responses rather than
appending a misleading error payload.

Request correlation is also treated as an HTTP trust boundary. Client-supplied
`X-Request-Id` values are accepted only when they are bounded visible ASCII.
Oversized or malformed values are discarded and the HTTP adapter generates a
cryptographically random request ID for logs and error responses. Generated
identifiers do not include the host/pod/container name or another topology
identifier.

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

- Core/HTTP: environment config, logging, error semantics, lifecycle, and an adapter-owned Huma/chi transport config.
- PostgreSQL: `internal/platform/database`, sqlc inputs/generated example store, migrations, and integration tests.
- Messaging: `internal/platform/messaging`, bounded NATS/JetStream connectivity, stream policy, publishing, durable consumer mechanics, and messaging integration tests.
- Telemetry: `internal/platform/telemetry`, OTLP exporters, SDK lifecycle, propagation, HTTP instrumentation, and bounded telemetry buffering/export policy.
- Outbox: `internal/platform/outbox`, caller-owned transactional enqueue, PostgreSQL lease/settlement primitives, and a bounded transport-neutral dispatcher. It does not import NATS.

A profile must be removable without forcing unrelated application code to understand it. PostgreSQL, NATS, and telemetry therefore default to disabled. The example sqlc package is not imported by the running application, and messaging handlers remain feature-owned rather than being embedded in the platform package.

Environment scalar parsing shared by multiple profiles lives in `internal/config/values.go`. A profile-specific config file must not own helpers required by unrelated profiles; removing an optional profile should not remove another profile's configuration primitives.

## PostgreSQL boundaries

The application composition root owns the pgx pool lifecycle. The database adapter owns pool configuration and bounded readiness checks. Use cases own transaction boundaries via `database.InTx`; repositories do not silently begin transactions.

The desired SQL schema and versioned migration history are both checked in CI:

1. sqlc regenerates from `sql/schema` + `sql/queries`;
2. Atlas verifies migration checksums;
3. migrations are applied to a fresh PostgreSQL instance;
4. Atlas compares the migrated database with the desired SQL schema while excluding its revisions table;
5. integration tests execute generated queries and transaction behavior.

See [database.md](database.md).

## Messaging boundaries

The application composition root owns the NATS connection lifecycle and combines NATS readiness with other enabled dependencies. The messaging adapter owns connection bounds, JetStream stream policy, synchronous deduplicated publishing, and durable pull-consumer mechanics. Readiness verifies the required stream against the same managed stream policy used at startup, so runtime drift in managed limits/subjects/storage policy is treated as not ready rather than merely checking stream existence.

The stdlib-only `internal/natssubject` and `internal/natsname` packages own
NATS subject grammar, literal-pattern matching, and JetStream identifier
validation shared by environment validation and the messaging adapter. Keeping
this protocol-specific policy outside `internal/core` avoids making core
depend on NATS while preventing the two startup boundaries from drifting.

Business handlers do not live in `internal/platform/messaging`. A feature owns its subject/filter/durable policy and passes its handler to the platform consumer. Normal shutdown drains buffered consumer work and then drains the NATS connection; both paths are bounded.

Cross-process context propagation is expressed through the stdlib-only `internal/core/propagation` contract. Telemetry implements the propagator, messaging implements the NATS header carrier, and `internal/app` wires them together only when both profiles are enabled.

Messaging also owns a small operation-tracing interface whose methods use only
`context.Context`, destination strings, and an error completion callback.
Telemetry satisfies that interface structurally, so NATS publishing/processing
can create spans without introducing an OpenTelemetry dependency into the
messaging adapter.

See [messaging.md](messaging.md).

## Telemetry boundaries

The application composition root owns the OpenTelemetry provider lifecycle. The telemetry adapter owns OTLP/HTTP exporters, SDK bounds, resource attributes, W3C propagation, and the HTTP instrumentation middleware.

The HTTP adapter exposes optional integration points through constructor options:
context-derived access-log attributes, matched-route observation, and outer
middleware. Route observation reports only chi route templates after routing;
the telemetry adapter converts them into low-cardinality HTTP span names and
`http.route` attributes without making the HTTP package import OpenTelemetry.

Access logging follows the same low-cardinality boundary: matched requests log
the chi route template, while unmatched requests log a fixed `<unmatched>`
marker. Raw `URL.Path` is deliberately excluded from default logs because path
segments may contain identifiers, personal data, secrets, or attacker-controlled
high-cardinality values. HTTP methods use the shared stdlib-only
`internal/httpmethod` policy: standard methods are preserved and every unknown
token is collapsed to the fixed `HTTP` bucket for both access logs and telemetry.

Telemetry is not a readiness dependency. Collector or backend failure may reduce observability, but it must not make a healthy service unavailable. Asynchronous SDK errors are logged without raw exporter/backend diagnostics.

HTTP observability callbacks follow the same availability rule. Panics from the
optional route observer or context-log-attribute callback are contained at the
adapter boundary, their panic values are discarded, and request processing plus
the normal access log continue with a sanitized warning.

Context-derived access-log attributes are an extension surface, not a second
owner of the access-log schema. The HTTP adapter drops empty/reserved keys
(`service`, `method`, `route`, `status`, `request_id`, and other
canonical record fields) and logs at most 16 accepted non-reserved callback
attributes per request. Reserved entries do not consume that allowance. This
keeps core fields authoritative and bounds extension-driven record growth.

Shutdown order is HTTP first, the outbox dispatcher second when enabled, NATS third when enabled, PostgreSQL fourth when enabled, and telemetry last so completed work can be flushed before process exit. HTTP shutdown first attempts graceful drain within `HTTP_SHUTDOWN_TIMEOUT`; if that context expires, the adapter force-closes active HTTP connections before dependency teardown continues. PostgreSQL close is independently bounded by `DATABASE_SHUTDOWN_TIMEOUT` because pgxpool close itself is not context-aware.

See [telemetry.md](telemetry.md).

## Transactional outbox boundary

The outbox closes the database/message-intent atomicity gap without making
PostgreSQL depend on NATS. Application use cases enqueue events inside their
existing caller-owned transaction.

Dispatch claiming uses `FOR UPDATE SKIP LOCKED`, commits the lease before
external I/O, and settles by row ID + lease token. The optional dispatcher
accepts a transport-neutral publisher function; only the application composition
root adapts that function to `messaging.Client.Publish`.

A claimed batch is published concurrently with explicit batch, lease, publish,
store-operation, retry, and shutdown bounds. Stable event IDs are reused as
broker deduplication IDs. Broker failures become finite retry/permanent-failure
state transitions, while storage or settlement failures stop the runtime rather
than hiding uncertain durable state.

Only `traceparent` and `tracestate` are persisted. Baggage is deliberately
excluded from durable outbox metadata.

See [outbox.md](outbox.md).

## Current bootstrap

The current increments provide:

- typed environment configuration with startup validation;
- structured `log/slog` logging;
- transport-neutral application error semantics;
- safe HTTP error translation with stable codes and request IDs;
- sanitized HTTP panic recovery without raw panic values or stack traces;
- build metadata and a version endpoint;
- a bounded HTTP server with explicit timeouts and request/body limits;
- graceful shutdown;
- Huma OpenAPI 3.1 generation on top of chi;
- liveness/readiness probes;
- optional PostgreSQL with pgx/sqlc/Atlas;
- optional NATS JetStream with bounded retry/quarantine, readiness, transport-neutral context propagation, and optional operation tracing;
- optional OpenTelemetry traces/metrics with OTLP/HTTP export, HTTP instrumentation, and W3C propagation plus publish/process spans across NATS when both profiles are enabled;
- optional transactional outbox dispatch with bounded claiming, concurrent publish, finite retry, and lifecycle wiring;
- unit, transport, database/Testcontainers, messaging, outbox, and telemetry export tests;
- Docker and CI quality gates.


## Outbox timing invariant

The outbox lease covers the entire bounded path from claim through broker
publish and durable settlement: one store timeout for claim, one publish
timeout, and one store timeout for settlement. The shared stdlib-only
`internal/outboxbudget` policy is used by both configuration validation and the
runtime dispatcher so those boundaries cannot drift independently.


## Stable message identifier invariant

The stdlib-only `internal/messageid` policy is shared by the messaging and
transactional-outbox boundaries. Non-empty stable IDs must survive text-header
serialization without normalization: leading/trailing ASCII whitespace and
CR/LF are rejected. This keeps durable outbox `event_id` values identical to
JetStream `Nats-Msg-Id` deduplication keys.
