// Package outboxbudget centralizes bounded dispatcher timing policy shared
// by configuration loading and the runtime outbox adapter.
package outboxbudget

import "time"

// LeaseCoversClaimPublishSettlement reports whether one outbox lease is strictly
// longer than the worst-case bounded claim, publish, and settlement sequence.
//
// The lease starts during Claim, so claim-side store time must be included in
// addition to the later publish and settlement operations.
func LeaseCoversClaimPublishSettlement(
	lease time.Duration,
	publishTimeout time.Duration,
	storeTimeout time.Duration,
) bool {
	return coversStrictly(
		lease,
		storeTimeout,
		publishTimeout,
		storeTimeout,
	)
}

// ShutdownCoversPublishSettlement reports whether shutdown leaves enough time
// for an already-claimed event to finish publish plus one settlement operation.
func ShutdownCoversPublishSettlement(
	shutdownTimeout time.Duration,
	publishTimeout time.Duration,
	storeTimeout time.Duration,
) bool {
	return coversStrictly(
		shutdownTimeout,
		publishTimeout,
		storeTimeout,
	)
}

func coversStrictly(window time.Duration, steps ...time.Duration) bool {
	if window <= 0 {
		return false
	}

	remaining := window
	for _, step := range steps {
		if step <= 0 || step >= remaining {
			return false
		}
		remaining -= step
	}
	return true
}
