package outboxbudget

import (
	"testing"
	"time"
)

func TestLeaseCoversClaimPublishSettlement(t *testing.T) {
	t.Parallel()

	store := 2 * time.Second
	publish := 5 * time.Second

	if LeaseCoversClaimPublishSettlement(9*time.Second, publish, store) {
		t.Fatal("exact claim+publish+settlement budget must not be accepted")
	}
	if LeaseCoversClaimPublishSettlement(9*time.Second+time.Nanosecond, publish, store) {
		t.Fatal("sub-microsecond lease slack must not survive storage precision")
	}
	if LeaseCoversClaimPublishSettlement(9*time.Second+999*time.Nanosecond, publish, store) {
		t.Fatal("sub-microsecond lease slack must be rejected")
	}
	if !LeaseCoversClaimPublishSettlement(9*time.Second+time.Microsecond, publish, store) {
		t.Fatal("one-microsecond persisted lease slack was rejected")
	}
}

func TestShutdownCoversPublishSettlement(t *testing.T) {
	t.Parallel()

	store := 2 * time.Second
	publish := 5 * time.Second

	if ShutdownCoversPublishSettlement(7*time.Second, publish, store) {
		t.Fatal("exact publish+settlement shutdown budget must not be accepted")
	}
	if !ShutdownCoversPublishSettlement(7*time.Second+time.Nanosecond, publish, store) {
		t.Fatal("strictly larger shutdown budget was rejected")
	}
}

func TestBudgetValidationRejectsInvalidAndOverflowLikeInputs(t *testing.T) {
	t.Parallel()

	maxDuration := time.Duration(1<<63 - 1)

	tests := []struct {
		name  string
		check bool
	}{
		{
			name:  "zero step",
			check: LeaseCoversClaimPublishSettlement(10*time.Second, time.Second, 0),
		},
		{
			name:  "negative step",
			check: LeaseCoversClaimPublishSettlement(10*time.Second, -time.Second, time.Second),
		},
		{
			name:  "huge publish timeout",
			check: LeaseCoversClaimPublishSettlement(24*time.Hour, maxDuration, maxDuration),
		},
	}
	for _, tt := range tests {
		if tt.check {
			t.Fatalf("%s unexpectedly fit within budget", tt.name)
		}
	}
}
