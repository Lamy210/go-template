-- align PostgreSQL outbox text constraints with the byte-oriented limits
-- enforced by the Go persistence boundary.
ALTER TABLE outbox_events
    DROP CONSTRAINT outbox_events_event_id_check,
    ADD CONSTRAINT outbox_events_event_id_check
        CHECK (octet_length(event_id) BETWEEN 1 AND 128),
    DROP CONSTRAINT outbox_events_subject_check,
    ADD CONSTRAINT outbox_events_subject_check
        CHECK (octet_length(subject) BETWEEN 1 AND 255),
    DROP CONSTRAINT outbox_events_traceparent_check,
    ADD CONSTRAINT outbox_events_traceparent_check
        CHECK (octet_length(traceparent) <= 256),
    DROP CONSTRAINT outbox_events_tracestate_check,
    ADD CONSTRAINT outbox_events_tracestate_check
        CHECK (octet_length(tracestate) <= 512);
