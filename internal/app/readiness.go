package app

import (
	"context"

	"github.com/Lamy210/go-template/internal/platform/httpserver"
)

// combineReadiness combines enabled dependency checks at the composition root.
// It returns nil when the service has no required readiness dependencies.
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
		for _, check := range active {
			if err := check(ctx); err != nil {
				return err
			}
		}
		return nil
	}
}
