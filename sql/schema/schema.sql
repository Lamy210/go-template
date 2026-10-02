CREATE TABLE example_items (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name TEXT NOT NULL CHECK (char_length(name) > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE outbox_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event_id TEXT NOT NULL UNIQUE
        CHECK (octet_length(event_id) BETWEEN 1 AND 128),
    subject TEXT NOT NULL
        CHECK (octet_length(subject) BETWEEN 1 AND 255),
    payload BYTEA NOT NULL
        CHECK (octet_length(payload) <= 1048576),
    traceparent TEXT NOT NULL DEFAULT ''
        CHECK (octet_length(traceparent) <= 256),
    tracestate TEXT NOT NULL DEFAULT ''
        CHECK (octet_length(tracestate) <= 512),
    attempts INTEGER NOT NULL DEFAULT 0
        CHECK (attempts >= 0),
    available_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    locked_until TIMESTAMPTZ,
    lock_token TEXT
        CHECK (lock_token IS NULL OR char_length(lock_token) <= 128),
    published_at TIMESTAMPTZ,
    failed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK (NOT (published_at IS NOT NULL AND failed_at IS NOT NULL)),
    CHECK ((locked_until IS NULL) = (lock_token IS NULL))
);

CREATE INDEX outbox_events_dispatch_idx
    ON outbox_events (available_at, id)
    WHERE published_at IS NULL AND failed_at IS NULL;
