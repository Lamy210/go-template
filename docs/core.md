# Core and Shared Code Policy

## Two meanings of "core"

The **Core profile** is the minimum production-ready capability set shared by every generated service: configuration, logging, error handling, graceful shutdown, build metadata, tests, lint/security gates, Docker, and CI.

The directory `internal/core` is narrower. It is reserved for stable, transport-neutral architecture primitives. A Core-profile capability does not automatically belong in `internal/core`.

For example:

- application error semantics belong in `internal/core/apperror`;
- build metadata belongs in `internal/buildinfo`;
- typed process configuration belongs in `internal/config`;
- HTTP response mapping belongs in `internal/platform/httpserver`.

## Purpose of `internal/core`

`internal/core` is not a dumping ground for reusable-looking code.

Code belongs in core only when all of the following are true:

1. it is independent of a specific business feature;
2. it is independent of HTTP, Huma, chi, PostgreSQL, NATS, cloud providers, and other adapters;
3. its semantics are stable across multiple modules;
4. moving it into core reduces duplication without hiding ownership;
5. it can be understood without importing a feature package.

The default is to keep code close to its owner.

## Current core

### `internal/core/apperror`

Defines transport-neutral application error semantics:

- `Kind`: coarse semantic classification used by adapters, with `Valid()` defining the known architecture-level set;
- `Code`: stable machine-readable application code;
- `Detail`: safe structured client-facing information;
- `Error`: safe public message plus an optional wrapped cause.

HTTP status codes are deliberately not part of `apperror`. The HTTP adapter owns that mapping.

The wrapped cause participates in the standard `errors.Is` / `errors.As` tree, but `Error()` does not include the cause text. This prevents a dependency error containing credentials, SQL, a DSN, or a token from being accidentally exposed through normal error formatting.

### `internal/core/safeerror`

Defines the shared infrastructure-error safety contract used by platform adapters:

- `Wrap(operation, cause)` keeps the original cause available to `errors.Is` / `errors.As`;
- ordinary `Error()` output contains only the safe operation name;
- a nil cause returns nil.

This is intentionally separate from `apperror`. `apperror` carries application semantics that transports may map to public responses; `safeerror` only prevents raw dependency diagnostics from being exposed accidentally while preserving the internal cause chain.

### `internal/core/propagation`

Defines the transport-neutral text-map propagation contract shared by adapters:

- `TextMapCarrier`: `Get`, `Set`, and `Keys` over transport headers;
- `TextMapPropagator`: context injection/extraction.

The package depends only on `context`. It deliberately does not import OpenTelemetry or NATS. The telemetry adapter implements the propagator and the messaging adapter implements the NATS header carrier, so either optional profile remains replaceable.

## Error ownership

Business/application code should return either:

- an `*apperror.Error` when callers or transports need semantic handling; or
- a wrapped Go error with useful internal context when no semantic mapping is required yet.

Platform adapters should use `safeerror.Wrap` when they need to retain a dependency cause whose raw text is unsafe for ordinary logs. Application semantic errors should continue to use `apperror`.

Transport adapters translate application errors into their protocol-specific representation.
They must fail closed when they encounter a semantic kind they do not explicitly
map. For example, HTTP converts an unmapped kind to the generic internal-error
contract instead of exposing its code, public message, or details. This prevents
a future or malformed kind from accidentally turning into a public 500 payload.

They also own protocol-specific panic containment. The HTTP adapter must not
emit raw panic values or stack traces; the messaging adapter converts feature
handler panics into a generic bounded-processing failure.

Do not:

- return Huma or HTTP errors from domain/application code;
- put HTTP status codes into core error kinds;
- expose raw dependency errors to clients;
- use error strings for programmatic branching;
- create a global error-code registry for every feature.

Feature-specific codes such as `user_not_found` should be declared close to that feature when they become shared constants.

## Common definitions

A type is not "common" merely because two files use it.

Prefer, in order:

1. an unexported type/function in the owning package;
2. an exported type in the owning feature package;
3. a narrowly named shared package when at least two independent consumers need the same stable semantics;
4. `internal/core` only when the type is genuinely architecture-level and transport-neutral.

Examples that may become core later, but are intentionally not added yet:

- clock abstraction, when business time becomes a real rule;
- ID generation port, when multiple implementations are required;
- pagination primitives, when multiple modules share cursor semantics.

## Helper policy

Do not create generic `utils`, `common`, `helpers`, or `misc` packages.

For helper code:

- keep a one-off helper unexported beside its caller;
- extract a package only when it represents a capability with a meaningful name;
- prefer the Go standard library over wrappers;
- do not wrap `errors.Is`, `errors.As`, slices, strings, maps, or context merely to shorten call sites;
- avoid generic helpers whose ownership, error behavior, or allocation cost is unclear.

Go 1.27 provides standard wrapped-error traversal, including `errors.AsType`, so this template does not add custom error-casting helpers.

## Dependency rule

Core may depend on the Go standard library.

Feature/application packages may depend on core.

Platform adapters may depend on core and application contracts.

Core must never depend on feature modules or platform adapters.
