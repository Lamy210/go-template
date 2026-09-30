package outbox

import (
	"context"

	coreprop "github.com/Lamy210/go-template/internal/core/propagation"
)

func injectPropagationSafely(
	ctx context.Context,
	propagator coreprop.TextMapPropagator,
) *propagationCarrier {
	if propagator == nil {
		return newPropagationCarrier()
	}

	carrier := newPropagationCarrier()
	ok := false
	func() {
		defer func() {
			if recover() != nil {
				return
			}
			ok = true
		}()
		propagator.Inject(ctx, carrier)
	}()
	if !ok {
		return newPropagationCarrier()
	}
	return carrier
}

func extractPropagationSafely(
	ctx context.Context,
	propagator coreprop.TextMapPropagator,
	carrier coreprop.TextMapCarrier,
) (result context.Context) {
	result = ctx
	if propagator == nil || carrier == nil {
		return result
	}

	defer func() {
		if recover() != nil {
			result = ctx
		}
	}()
	extracted := propagator.Extract(ctx, carrier)
	if extracted != nil {
		result = extracted
	}
	return result
}
