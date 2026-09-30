package messaging

import (
	"testing"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

func TestMessageFitsPublishLimitIncludesSerializedHeaders(t *testing.T) {
	t.Parallel()

	msg := nats.NewMsg("events.created")
	msg.Data = []byte("0123456789")
	msg.Header.Set(jetstream.MsgIDHeader, "event-1")

	// NATS/1.0\r\n + final CRLF = 12 bytes.
	// "Nats-Msg-Id: event-1\r\n" = 22 bytes.
	// Payload = 10 bytes.
	const exactSize = int64(44)
	if !messageFitsPublishLimit(msg, exactSize) {
		t.Fatal("message did not fit its exact conservative wire-size budget")
	}
	if messageFitsPublishLimit(msg, exactSize-1) {
		t.Fatal("message fit a publish limit smaller than its header+payload size")
	}
}

func TestMessageFitsPublishLimitWithoutHeadersUsesPayloadOnly(t *testing.T) {
	t.Parallel()

	msg := nats.NewMsg("events.created")
	msg.Data = []byte("payload")

	if !messageFitsPublishLimit(msg, int64(len(msg.Data))) {
		t.Fatal("headerless message did not fit exact payload limit")
	}
	if messageFitsPublishLimit(msg, int64(len(msg.Data)-1)) {
		t.Fatal("headerless message fit undersized limit")
	}
}

func TestPublishLimitsUseSmallestMatchingBound(t *testing.T) {
	t.Parallel()

	var limits publishLimits
	limits.remember("EVENTS", []string{"events.>"}, 1024)
	limits.remember("OTHER", []string{"other.*"}, 2048)

	if got := limits.forSubject("events.created", 4096); got != 1024 {
		t.Fatalf("events limit = %d, want 1024", got)
	}
	if got := limits.forSubject("other.created", 1024); got != 1024 {
		t.Fatalf("other limit = %d, want broker limit 1024", got)
	}
	if got := limits.forSubject("unmanaged.created", 4096); got != 4096 {
		t.Fatalf("unmanaged limit = %d, want broker limit 4096", got)
	}
}

func TestPublishLimitsTreatNonPositiveBrokerLimitAsUnbounded(t *testing.T) {
	t.Parallel()

	var limits publishLimits
	limits.remember("EVENTS", []string{"events.>"}, 1024)

	if got := limits.forSubject("events.created", 0); got != 1024 {
		t.Fatalf("managed limit = %d, want 1024", got)
	}
	if got := limits.forSubject("unmanaged.created", -1); got != 0 {
		t.Fatalf("unmanaged no-limit result = %d, want 0", got)
	}
}
