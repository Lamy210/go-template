package messaging

import (
	"testing"

	"github.com/nats-io/nats.go"
)

func TestNATSHeaderCarrierGetIsCaseInsensitive(t *testing.T) {
	t.Parallel()

	carrier := natsHeaderCarrier{
		header: nats.Header{
			"Traceparent": {"00-0123456789abcdef0123456789abcdef-0123456789abcdef-01"},
		},
	}

	got := carrier.Get("traceparent")
	want := "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01"
	if got != want {
		t.Fatalf("Get(traceparent) = %q, want %q", got, want)
	}
}

func TestNATSHeaderCarrierSetReusesExistingHeaderCase(t *testing.T) {
	t.Parallel()

	header := nats.Header{
		"Traceparent": {"old"},
	}
	carrier := natsHeaderCarrier{header: header}

	carrier.Set("traceparent", "new")

	if got := header.Get("Traceparent"); got != "new" {
		t.Fatalf("Traceparent = %q, want new", got)
	}
	if _, exists := header["traceparent"]; exists {
		t.Fatal("Set created duplicate traceparent key with different casing")
	}
}

func TestNATSHeaderCarrierKeysAreSorted(t *testing.T) {
	t.Parallel()

	carrier := natsHeaderCarrier{
		header: nats.Header{
			"traceparent": {"trace"},
			"baggage":     {"a=b"},
		},
	}

	got := carrier.Keys()
	if len(got) != 2 || got[0] != "baggage" || got[1] != "traceparent" {
		t.Fatalf("Keys() = %#v, want sorted baggage/traceparent", got)
	}
}
