-- keep durable W3C tracestate coupled to a persisted traceparent.
UPDATE outbox_events
SET tracestate = ''
WHERE traceparent = '' AND tracestate <> '';

ALTER TABLE outbox_events
    ADD CONSTRAINT outbox_events_trace_context_state_check
    CHECK (tracestate = '' OR traceparent <> '');
