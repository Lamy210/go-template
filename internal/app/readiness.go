package app

import (
	"context"

	"github.com/Lamy210/go-template/internal/platform/httpserver"
)

type readinessResult struct {
	err error
}

// combineReadiness combines enabled dependency checks at the composition root.
// It returns nil when the service has no required readiness dependencies.
//
// Checks run concurrently so readiness latency is bounded by the slowest
// dependency rather than the sum of all dependency latencies. Checks are
// expected to honor context cancellation.
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

		results := make(chan readinessResult, len(active))
		for _, check := range active {
			check := check
			go func() {
				results <- readinessResult{err: check(checkCtx)}
			}()
		}

		var firstErr error
		for range active {
			result := <-results
			if result.err == nil || firstErr != nil {
				continue
			}
			firstErr = result.err
			cancel()
		}
		return firstErr
	}
}
