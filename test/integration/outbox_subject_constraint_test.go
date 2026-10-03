package integration

import (
	"context"
	"fmt"
	"testing"
)

func TestOutboxSubjectConstraintRejectsWhitespaceOnlyText(t *testing.T) {
	pool, ctx := openTestPool(t)

	whitespace := []rune{
		'\t', '\n', '\v', '\f', '\r', ' ',
		'\u0085', '\u00a0', '\u1680',
		'\u2000', '\u2001', '\u2002', '\u2003', '\u2004', '\u2005',
		'\u2006', '\u2007', '\u2008', '\u2009', '\u200a',
		'\u2028', '\u2029', '\u202f', '\u205f', '\u3000',
	}
	for i, subject := range whitespace {
		if _, err := pool.Exec(
			ctx,
			`INSERT INTO outbox_events (event_id, subject, payload)
			 VALUES ($1, $2, ''::bytea)`,
			fmt.Sprintf("blank-subject-%02d", i),
			string(subject),
		); err == nil {
			t.Fatalf("insert with whitespace-only subject %U succeeded", subject)
		}
	}

	const eventID = "subject-with-non-space"
	const transportNeutralSubject = " events test "
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
