CREATE TABLE example_items (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name TEXT NOT NULL CHECK (char_length(name) > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE outbox_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event_id TEXT NOT NULL UNIQUE
        CHECK (
            octet_length(event_id) BETWEEN 1 AND 128
            AND event_id = btrim(event_id, E' \t\r\n')
            AND position(E'\r' in event_id) = 0
            AND position(E'\n' in event_id) = 0
        ),
    subject TEXT NOT NULL
        CHECK (
            octet_length(subject) BETWEEN 1 AND 255
            AND translate(
                subject,
                U&'\0009\000A\000B\000C\000D\0020\0085\00A0\1680\2000\2001\2002\2003\2004\2005\2006\2007\2008\2009\200A\2028\2029\202F\205F\3000',
                ''
            ) <> ''
        ),
    payload BYTEA NOT NULL
        CHECK (octet_length(payload) <= 1048576),
    traceparent TEXT NOT NULL DEFAULT ''
        CHECK (octet_length(traceparent) <= 256),
    tracestate TEXT NOT NULL DEFAULT ''
        CHECK (octet_length(tracestate) <= 512),
    attempts INTEGER NOT NULL DEFAULT 0
        CHECK (attempts >= 0),
    retry_scheduled BOOLEAN NOT NULL DEFAULT FALSE,
    available_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    locked_until TIMESTAMPTZ,
    lock_token TEXT
        CHECK (lock_token IS NULL OR char_length(lock_token) <= 128),
    published_at TIMESTAMPTZ,
    failed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK (NOT (published_at IS NOT NULL AND failed_at IS NOT NULL)),
    CHECK ((locked_until IS NULL) = (lock_token IS NULL)),
    CONSTRAINT outbox_events_settled_attempt_check
        CHECK (
            (published_at IS NULL AND failed_at IS NULL)
            OR attempts > 0
        ),
    CONSTRAINT outbox_events_trace_context_state_check
        CHECK (tracestate = '' OR traceparent <> ''),
    CONSTRAINT outbox_events_lease_state_check
        CHECK (
            locked_until IS NULL
            OR (
                attempts > 0
                AND lock_token <> ''
                AND NOT retry_scheduled
                AND published_at IS NULL
                AND failed_at IS NULL
            )
        ),
    CONSTRAINT outbox_events_retry_scheduled_state_check
        CHECK (
            NOT retry_scheduled
            OR (
                attempts > 0
                AND locked_until IS NULL
                AND lock_token IS NULL
                AND published_at IS NULL
                AND failed_at IS NULL
            )
        )
);

CREATE INDEX outbox_events_dispatch_idx
    ON outbox_events (available_at, id)
    WHERE published_at IS NULL AND failed_at IS NULL;
