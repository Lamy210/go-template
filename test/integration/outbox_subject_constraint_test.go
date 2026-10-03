package integration

import (
	"context"
	"testing"
)

func TestOutboxSubjectConstraintRejectsWhitespaceOnlyText(t *testing.T) {
	pool, ctx := openTestPool(t)

	for i, subject := range []string{
		" ",
		"\t\n\r\v\f",
		"\u0085",
		"\u00a0",
		"\u1680",
		"\u2003",
		"\u2028",
		"\u2029",
		"\u202f",
		"\u205f",
		"\u3000",
	} {
		if _, err := pool.Exec(
			ctx,
			`INSERT INTO outbox_events (event_id, subject, payload)
			 VALUES ($1, $2, ''::bytea)`,
			"blank-subject-"+string(rune('a'+i)),
			subject,
		); err == nil {
			t.Fatalf("insert with whitespace-only subject %q succeeded", subject)
		}
	}

	const eventID = "subject-with-non-space"
	const transportNeutralSubject = "events test"
	if _, err := pool.Exec(
		ctx,
		`INSERT INTO outbox_events (event_id, subject, payload)
		 VALUES ($1, $2, ''::bytea)`,
		eventID,
		transportNeutralSubject,
	); err != nil {
		t.Fatalf("insert transport-neutral nonblank subject: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(
			context.Background(),
			"DELETE FROM outbox_events WHERE event_id = $1",
			eventID,
		)
	})
}
