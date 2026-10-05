-- require terminal outbox states to follow at least one claim.
ALTER TABLE outbox_events
    ADD CONSTRAINT outbox_events_settled_attempt_check
    CHECK (
        (published_at IS NULL AND failed_at IS NULL)
        OR attempts > 0
    );
