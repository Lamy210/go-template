package messaging

import (
	"context"
	"testing"
	"time"
)

func TestPublishRejectsInvalidLiteralSubjectBeforeBrokerUse(t *testing.T) {
	t.Parallel()

	client := &Client{}
	for _, subject := range []string{
		"",
		"events.*",
		"events.>",
		"events..created",
		"events created",
	} {
		if _, err := client.Publish(
			context.Background(),
			subject,
			"event-1",
			[]byte("payload"),
		); err == nil {
			t.Fatalf("Publish(%q) error = nil, want invalid subject error", subject)
		}
	}
}

func TestPublishRejectsNormalizedMessageIDBeforeBrokerUse(t *testing.T) {
	t.Parallel()

	client := &Client{}
	for _, msgID := range []string{
		" event-1",
		"event-1 ",
		"\tevent-1",
		"event-1\t",
		"event\n1",
		"event\r1",
	} {
		if _, err := client.Publish(
			context.Background(),
			"events.created",
			msgID,
			[]byte("payload"),
		); err == nil {
			t.Fatalf("Publish(msgID=%q) error = nil, want canonical ID error", msgID)
		}
	}
}

func TestReadinessCheckRejectsInvalidBoundaryInputs(t *testing.T) {
	t.Parallel()

	stream := testConsumerConfig().Stream

	if err := (&Client{}).ReadinessCheck(stream, 0)(context.Background()); err == nil {
		t.Fatal("ReadinessCheck() timeout error = nil")
	}

	if err := (&Client{}).ReadinessCheck(stream, time.Second)(context.Background()); err == nil {
		t.Fatal("ReadinessCheck() uninitialized client error = nil")
	}
}
