// Package natsbudget centralizes bounded JetStream consumer timing policy
// shared by configuration loading and the runtime messaging adapter.
package natsbudget

import "time"

// AckWaitCoversSettlement reports whether one JetStream acknowledgement window
// is strictly longer than the worst-case handler, quarantine publish, and
// acknowledgement sequence.
//
// Subtraction is used instead of summing durations so validation remains safe at
// time.Duration's integer limits.
func AckWaitCoversSettlement(
	ackWait time.Duration,
	handlerTimeout time.Duration,
	publishTimeout time.Duration,
	ackTimeout time.Duration,
) bool {
	if ackWait <= 0 {
		return false
	}

	remaining := ackWait
	for _, phase := range []time.Duration{
		handlerTimeout,
		publishTimeout,
		ackTimeout,
	} {
		if phase <= 0 || phase >= remaining {
			return false
		}
		remaining -= phase
	}
	return remaining > 0
}
