# OpenTelemetry Profile

The telemetry profile is optional. `TELEMETRY_ENABLED=false` is the default,
and disabled services do not parse or validate telemetry-specific settings.

## Scope

The initial profile covers:

- distributed traces;
- metrics;
- OTLP over HTTP/protobuf;
- W3C Trace Context and Baggage propagation;
- inbound `net/http` instrumentation through `otelhttp`;
- service resource attributes.

OpenTelemetry logs are deliberately not enabled. Application logging continues
to use structured `log/slog`. This keeps the template on stable OTel signals
while avoiding a second logging pipeline.

When telemetry is enabled, HTTP access logs include the active `trace_id` and
`span_id` so operators can correlate structured logs with exported traces
without adopting the OpenTelemetry logging signal.

## Export topology

Use an OpenTelemetry Collector or OTLP-compatible backend:

```text
service
  ├─ /v1/traces  ─┐
  └─ /v1/metrics ─┴─> OTLP/HTTP collector/backend
```

`OTEL_EXPORTER_OTLP_ENDPOINT` is treated as the base URL. For example,
`https://collector.example.com/otel` becomes:

- `https://collector.example.com/otel/v1/traces`;
- `https://collector.example.com/otel/v1/metrics`.

The initial profile intentionally uses one shared endpoint rather than separate
per-signal endpoints.

## Bounded defaults

The SDK does not rely on unbounded buffering or retry behavior:

- export timeout: 15s;
- retry initial interval: 500ms;
- retry max interval: 2s;
- retry max elapsed time: 10s;
- metric export interval: 30s;
- trace max queue: 2048 spans;
- trace max export batch: 512 spans;
- trace batch timeout: 5s;
- max serialized OTLP request: 4 MiB;
- shutdown timeout: 10s;
- trace sampling ratio: 1.0.

All values are configurable through `.env.example`. Validation rejects a retry
window longer than the export deadline, a trace batch larger than its queue,
invalid sampling ratios, zero/unbounded request limits, and malformed endpoints.

## Readiness policy

Telemetry is deliberately **not** included in `/health/ready`.

Database and messaging dependencies can be required for application correctness.
An observability collector is different: making collector loss fail readiness can
turn an observability incident into an application availability incident.

Exporter failures are therefore handled asynchronously. The global OTel error
handler emits a generic warning and does not log the raw exporter error, because
backend diagnostics can contain endpoint or response details.

## Resource attributes

The provider attaches stable semantic-convention attributes:

- `service.name`;
- `service.version`;
- `deployment.environment.name`.

## HTTP instrumentation

The application passes the telemetry middleware into the HTTP adapter as an
outer middleware. The HTTP package does not import OpenTelemetry itself. This
keeps dependency direction explicit and lets the telemetry profile remain
runtime-optional.

The HTTP adapter also accepts an optional context-to-log-attributes callback.
The telemetry profile supplies this callback to extract correlation identifiers
from the active span; when telemetry is disabled, the callback is nil and no
trace fields are emitted.

## Messaging propagation

The provider also implements the transport-neutral text-map propagation contract
from `internal/core/propagation`. When the NATS profile is enabled at the same
time, the application passes the provider to the messaging client.

That lets JetStream publishers inject W3C Trace Context/Baggage into NATS
headers and consumers restore it before invoking feature handlers. The messaging
package does not import OpenTelemetry, and the telemetry package does not import
NATS.

Quarantine messages receive a freshly injected propagation header set from the
extracted context instead of copying arbitrary original headers.

Because Baggage is propagated through message headers, its contents can be
persisted with JetStream messages and observed by infrastructure operators. Do
not place credentials, access tokens, personal data, or unbounded/high-cardinality
values in Baggage.

## Shutdown

Normal shutdown is ordered:

1. stop accepting/drain HTTP requests;
2. drain NATS when enabled;
3. flush and shut down trace and metric providers.

Telemetry shutdown is bounded by `TELEMETRY_SHUTDOWN_TIMEOUT`. Startup failure
after telemetry initialization also triggers a bounded fail-safe shutdown.

## Security

Do not put credentials in `OTEL_EXPORTER_OTLP_ENDPOINT`; URL userinfo is
rejected. Use standard OTLP header environment variables or the collector's
supported authentication mechanism instead.

The application never logs the configured endpoint or raw exporter errors.
