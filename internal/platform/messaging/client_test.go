package messaging

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestApplyOptionSafelyContainsOptionPanic(t *testing.T) {
	t.Parallel()

	const sensitive = "sensitive option panic"
	err := applyOptionSafely(&Client{}, func(*Client) {
		panic(sensitive)
	})
	if !errors.Is(err, errClientOptionPanic) {
		t.Fatalf("applyOptionSafely() error = %v, want option panic sentinel", err)
	}
	if strings.Contains(err.Error(), sensitive) {
		t.Fatalf("applyOptionSafely() exposed panic value: %q", err.Error())
	}
}

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
		_, err := client.Publish(
			context.Background(),
			subject,
			"event-1",
			[]byte("payload"),
		)
		if !errors.Is(err, ErrInvalidPublishSubject) {
			t.Fatalf(
				"Publish(%q) error = %v, want ErrInvalidPublishSubject",
				subject,
				err,
			)
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
		_, err := client.Publish(
			context.Background(),
			"events.created",
			msgID,
			[]byte("payload"),
		)
		if !errors.Is(err, ErrInvalidMessageID) {
			t.Fatalf(
				"Publish(msgID=%q) error = %v, want ErrInvalidMessageID",
				msgID,
				err,
			)
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
