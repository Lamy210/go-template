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
		t.Fatal("strictly larger AckWait was rejected")
	}
}

func TestSettlementDurationRejectsInvalidAndOverflowingInputs(t *testing.T) {
	t.Parallel()

	max := time.Duration(1<<63 - 1)
	tests := []struct {
		name    string
		handler time.Duration
		publish time.Duration
		ack     time.Duration
	}{
		{
			name:    "zero phase",
			handler: time.Second,
			publish: 0,
			ack:     time.Second,
		},
		{
			name:    "negative phase",
			handler: time.Second,
			publish: time.Second,
			ack:     -time.Second,
		},
		{
			name:    "overflow",
			handler: max - time.Second,
			publish: time.Second,
			ack:     time.Nanosecond,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, ok := SettlementDuration(tt.handler, tt.publish, tt.ack); ok {
				t.Fatal("SettlementDuration() accepted invalid budget")
			}
		})
	}
}

func TestSettlementDurationReturnsExactSum(t *testing.T) {
	t.Parallel()

	got, ok := SettlementDuration(
		30*time.Second,
		5*time.Second,
		5*time.Second,
	)
	if !ok {
		t.Fatal("SettlementDuration() rejected valid phases")
	}
	if got != 40*time.Second {
		t.Fatalf("SettlementDuration() = %s, want 40s", got)
	}
}
