package messaging

import (
	"context"
	"testing"
	"time"
)

func TestPublicClientMethodsRejectUninitializedClient(t *testing.T) {
	t.Parallel()

	stream := testConsumerConfig().Stream
	consumer := testConsumerConfig()
	clients := []*Client{nil, {}}

	for _, client := range clients {
		if _, err := client.Publish(
			context.Background(),
			"events.created",
			"event-1",
			[]byte("payload"),
		); err == nil {
			t.Fatal("Publish() error = nil, want uninitialized client error")
		}

		if err := client.EnsureStream(context.Background(), stream); err == nil {
			t.Fatal("EnsureStream() error = nil, want uninitialized client error")
		}

		if err := client.RunConsumer(
			context.Background(),
			consumer,
			func(context.Context, Message) error { return nil },
		); err == nil {
			t.Fatal("RunConsumer() error = nil, want uninitialized client error")
		}

		if err := client.ReadinessCheck(stream, time.Second)(context.Background()); err == nil {
			t.Fatal("ReadinessCheck() error = nil, want uninitialized client error")
		}

		if err := client.Drain(context.Background()); err == nil {
			t.Fatal("Drain() error = nil, want uninitialized client error")
		}

		client.Close()
	}
}

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
