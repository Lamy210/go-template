-- keep the one-shot retry authorization in its valid durable state only.
ALTER TABLE outbox_events
    ADD CONSTRAINT outbox_events_retry_scheduled_state_check
    CHECK (
        NOT retry_scheduled
        OR (
            attempts > 0
            AND locked_until IS NULL
            AND lock_token IS NULL
            AND published_at IS NULL
            AND failed_at IS NULL
        )
    );
