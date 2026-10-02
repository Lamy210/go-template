// Package natsbudget centralizes bounded JetStream consumer timing policy
// shared by configuration loading and the runtime messaging adapter.
package natsbudget

import "time"

const maxDuration = time.Duration(1<<63 - 1)

// SettlementDuration returns the worst-case bounded duration of a final
// processing attempt that runs the handler, publishes to quarantine after a
// failure, and confirms the original delivery with DoubleAck.
//
// The boolean is false when a phase is non-positive or the sum would overflow a
// time.Duration.
func SettlementDuration(
	handlerTimeout time.Duration,
	publishTimeout time.Duration,
	ackTimeout time.Duration,
) (time.Duration, bool) {
	var total time.Duration
	for _, phase := range []time.Duration{
		handlerTimeout,
		publishTimeout,
		ackTimeout,
	} {
		if phase <= 0 || phase > maxDuration-total {
			return 0, false
		}
		total += phase
	}
	return total, true
}

// AckWaitCoversSettlement reports whether JetStream AckWait is strictly longer
// than the full bounded handler, quarantine-publish, and acknowledgement
// sequence.
func AckWaitCoversSettlement(
	ackWait time.Duration,
	handlerTimeout time.Duration,
	publishTimeout time.Duration,
	ackTimeout time.Duration,
) bool {
	settlement, ok := SettlementDuration(handlerTimeout, publishTimeout, ackTimeout)
	return ok && ackWait > settlement
}
