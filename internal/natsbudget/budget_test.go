package natsbudget

import (
	"testing"
	"time"
)

func TestAckWaitCoversSettlement(t *testing.T) {
	t.Parallel()

	handler := 30 * time.Second
	publish := 5 * time.Second
	ack := 5 * time.Second

	if AckWaitCoversSettlement(40*time.Second, handler, publish, ack) {
		t.Fatal("exact handler+publish+ack budget must not be accepted")
	}
	if !AckWaitCoversSettlement(40*time.Second+time.Nanosecond, handler, publish, ack) {
		t.Fatal("strictly larger acknowledgement window must be accepted")
	}
}

func TestAckWaitCoversSettlementRejectsInvalidPhases(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name    string
		ackWait time.Duration
		handler time.Duration
		publish time.Duration
		ack     time.Duration
	}{
		{name: "zero ack wait", handler: time.Second, publish: time.Second, ack: time.Second},
		{name: "zero handler", ackWait: 4 * time.Second, publish: time.Second, ack: time.Second},
		{name: "zero publish", ackWait: 4 * time.Second, handler: time.Second, ack: time.Second},
		{name: "zero ack", ackWait: 4 * time.Second, handler: time.Second, publish: time.Second},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if AckWaitCoversSettlement(tt.ackWait, tt.handler, tt.publish, tt.ack) {
				t.Fatal("AckWaitCoversSettlement() = true, want false")
			}
		})
	}
}
