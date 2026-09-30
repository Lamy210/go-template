package app

import (
	"context"
	"errors"

	"github.com/Lamy210/go-template/internal/platform/httpserver"
)

// combineReadiness combines enabled dependency checks at the composition root.
// It returns nil when the service has no required readiness dependencies.
//
// Checks run concurrently so readiness latency is bounded by the slowest
// successful dependency rather than the sum of all dependency latencies. The
// first failure cancels siblings and returns immediately. Checks must honor
// context cancellation so fail-fast probes do not leave stuck work behind. A
// check panic is converted to a generic failure instead of crashing the process.
func combineReadiness(checks ...httpserver.ReadinessCheck) httpserver.ReadinessCheck {
	active := make([]httpserver.ReadinessCheck, 0, len(checks))
	for _, check := range checks {
		if check != nil {
			active = append(active, check)
		}
	}
	if len(active) == 0 {
		return nil
	}

	return func(ctx context.Context) error {
		checkCtx, cancel := context.WithCancel(ctx)
		defer cancel()

		results := make(chan error, len(active))
		for _, check := range active {
			go func(check httpserver.ReadinessCheck) {
				results <- invokeReadinessCheck(checkCtx, check)
			}(check)
		}

		for range active {
			select {
			case err := <-results:
				if err != nil {
					cancel()
					return err
				}
			case <-ctx.Done():
				cancel()
				return ctx.Err()
			}
		}
		return nil
	}
}

var errReadinessCheckPanic = errors.New("readiness check panicked")

func invokeReadinessCheck(
	ctx context.Context,
	check httpserver.ReadinessCheck,
) (err error) {
	defer func() {
		if recover() != nil {
			err = errReadinessCheckPanic
		}
	}()
	return check(ctx)
}
