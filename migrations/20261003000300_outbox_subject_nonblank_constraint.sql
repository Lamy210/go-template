-- keep durable outbox subjects nonblank across direct SQL and Go enqueue paths.
ALTER TABLE outbox_events
    DROP CONSTRAINT outbox_events_subject_check,
    ADD CONSTRAINT outbox_events_subject_check
        CHECK (
            octet_length(subject) BETWEEN 1 AND 255
            AND translate(
                subject,
                U&'\0009\000A\000B\000C\000D\0020\0085\00A0\1680\2000\2001\2002\2003\2004\2005\2006\2007\2008\2009\200A\2028\2029\202F\205F\3000',
                ''
            ) <> ''
        );
