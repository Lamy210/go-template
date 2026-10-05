-- keep active leases tied to claimed, unsettled outbox events only.
ALTER TABLE outbox_events
    ADD CONSTRAINT outbox_events_lease_state_check
    CHECK (
        locked_until IS NULL
        OR (
            attempts > 0
            AND lock_token <> ''
            AND NOT retry_scheduled
            AND published_at IS NULL
            AND failed_at IS NULL
        )
    );
