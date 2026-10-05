-- distinguish explicitly scheduled retries from expired-lease crash recovery.
ALTER TABLE outbox_events
    ADD COLUMN retry_scheduled BOOLEAN NOT NULL DEFAULT FALSE;
