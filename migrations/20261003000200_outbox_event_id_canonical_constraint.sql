-- keep durable event IDs canonical across direct SQL and Go enqueue paths.
ALTER TABLE outbox_events
    DROP CONSTRAINT outbox_events_event_id_check,
    ADD CONSTRAINT outbox_events_event_id_check
        CHECK (
            octet_length(event_id) BETWEEN 1 AND 128
            AND event_id = btrim(event_id, E' \t\r\n')
            AND position(E'\r' in event_id) = 0
            AND position(E'\n' in event_id) = 0
        );
