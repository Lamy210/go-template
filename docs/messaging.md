# Async Worker / NATS JetStream Profile

The NATS profile is optional. `NATS_ENABLED=false` is the default, and a
service that does not use messaging does not parse or validate NATS-specific
settings.

## Stack

- nats-server 2.15.x
- nats.go 1.54.x
- modern `github.com/nats-io/nats.go/jetstream` API

The profile deliberately overrides unbounded server/client defaults.

## Bounded defaults

The NATS client default reconnect limit is finite, but the template still sets
it explicitly. Negative reconnect counts are rejected because they mean retry
forever.

JetStream consumers are also configured explicitly:

- explicit acknowledgements;
- finite processing attempts;
- finite quarantine-publish attempts;
- finite `MaxAckPending`;
- bounded handler and ack timeouts;
- `AckWait` strictly greater than `HandlerTimeout + AckTimeout`;
- delayed retry rather than immediate hot-loop redelivery.

The consumer currently does not send periodic `InProgress` acknowledgements.
JetStream redelivers an unacknowledged message after `AckWait`, so the template
reserves enough acknowledgement budget for the full bounded handler execution
plus the bounded `DoubleAck` confirmation. The default is therefore
`AckWait=40s`, `HandlerTimeout=30s`, and `AckTimeout=5s`, leaving 5 seconds
of scheduling/network slack. Invalid combinations fail validation before the
consumer starts.

JetStream streams are created with finite message count, byte, age, consumer,
and single-message-size limits. The template never relies on JetStream's
unlimited `MaxMsgs` / `MaxBytes` defaults.

## Runtime wiring

When `NATS_ENABLED=true`, the application composition root:

1. creates one bounded NATS connection;
2. creates or reconciles the configured JetStream stream;
3. includes NATS/JetStream in `/health/ready` alongside other enabled
   required dependencies, verifying both that the configured required stream
   still exists and that the stream fields managed by this template have not
   drifted;
4. drains HTTP first and then drains NATS during normal shutdown;
5. keeps an immediate connection close as a fail-safe for startup failure or
   abnormal exit.

The application does not register a fake business consumer. Consumer handlers
belong to a feature/application package and call `RunConsumer` with the
durable/filter policy they own. This keeps the NATS profile reusable and
prevents infrastructure code from becoming a business `worker` dumping
ground.

## Idempotent publishing

`messaging.Client.Publish` accepts a stable message ID and maps it to
JetStream's message-ID deduplication. Callers should use an operation/event ID,
not generate a new ID on every retry.

## Tracing and context propagation

Messaging accepts optional transport-neutral propagation and operation-tracing
interfaces. When both NATS and telemetry profiles are enabled, the application
composition root wires the OpenTelemetry provider into both interfaces.

Each synchronous publish creates a PRODUCER span before propagation headers are
injected, so the publish span context becomes the message creation context. Each
business handler attempt creates a CONSUMER process span after extracting the
message context. The process span remains open through message settlement: a
successful handler is not reported as successful until `DoubleAck` succeeds,
and retry/quarantine decisions occur before a failed process span is closed.
Quarantine publishing also goes through the same publish path and, when triggered
directly by a failed handler attempt, is parented to that process operation.

The messaging package itself does not import OpenTelemetry. Feature handlers
only receive a standard `context.Context`.

When telemetry is disabled, no propagator is installed and messaging behavior is
otherwise unchanged.

Propagation headers are part of the persisted message metadata. Do not put
credentials, access tokens, personal data, or unbounded/high-cardinality values
in OpenTelemetry Baggage. Treat Baggage as broker-visible metadata and keep it
small enough for normal NATS header limits and operational inspection.

## Retry and quarantine

`RunConsumer` separates normal processing attempts from quarantine attempts.

1. A handler failure is retried with `NakWithDelay` while normal attempts remain.
2. After normal attempts are exhausted, the original payload is synchronously
   published to the configured quarantine subject.
3. The quarantine publish uses a stable ID derived from stream + sequence, so a
   repeated quarantine attempt is deduplicated.
4. Only after the quarantine publish succeeds is the original message
   acknowledged with `DoubleAck`.
5. If quarantine publishing repeatedly fails, the worker returns an
   infrastructure error instead of retrying forever.

Handler error text is not copied into quarantine headers or generic
infrastructure logs.

A handler must honor its context cancellation promptly. If it returns `nil`
after its handler context has already expired, the messaging boundary treats the
expired context as a processing failure instead of acknowledging the delivery as
successful. That failure follows the same bounded retry/quarantine policy.

A panic from a feature handler is contained at the messaging boundary and
converted to a generic typed handler failure. The panic value itself is
discarded rather than logged or copied into telemetry/quarantine metadata. The
delivery then follows the same finite retry and quarantine policy as an ordinary
handler error, preventing a poison message from repeatedly crashing the whole
worker process.

## Graceful shutdown

The consumer uses JetStream `ConsumeContext.Drain` on normal cancellation so
buffered deliveries can finish. Handler and acknowledgement work remains
bounded by explicit timeouts. The NATS connection then has its own bounded
drain lifecycle. A broker-side drain timeout is surfaced as a safe operation
error instead of being silently treated as a successful close.

This avoids leaving a live consume goroutine after process shutdown.

## Local development

```bash
docker compose up -d nats
```

JetStream is enabled on port 4222. The monitoring endpoint is exposed on 8222
for local diagnostics.

## Integration tests

The messaging CI job starts a real JetStream-enabled nats-server and verifies:

- stream provisioning with explicit limits;
- message-ID deduplication;
- publish-to-handler context propagation through real NATS headers;
- publish/process tracer lifecycle and operation-context propagation;
- panic containment followed by bounded retry without crashing the consumer;
- handler deadline expiry followed by retry even when the handler returns nil;
- delayed retry followed by successful acknowledgement;
- bounded failure followed by quarantine with propagation preserved;
- consumer drain on cancellation;
- NATS/JetStream readiness while the required stream exists with the managed configuration;
- readiness failure after a managed stream field drifts;
- readiness recovery after the managed stream configuration is restored;
- readiness failure after the required stream is deleted.

The profile remains independent of PostgreSQL. When both profiles are enabled,
their readiness checks are composed at the application boundary rather than
making either infrastructure package depend on the other.
