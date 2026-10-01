package outbox

import (
	"context"
	"strings"
	"unicode/utf8"

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

func persistedPropagationMetadata(carrier *propagationCarrier) (string, string) {
	if carrier == nil {
		return "", ""
	}

	traceparent := carrier.Get("traceparent")
	if traceparent == "" || !safePropagationText(traceparent, maxTraceparentLen) {
		return "", ""
	}

	tracestate := carrier.Get("tracestate")
	if tracestate != "" && !safePropagationText(tracestate, maxTracestateLen) {
		tracestate = ""
	}
	return traceparent, tracestate
}

func safePropagationText(value string, maxLen int) bool {
	return len(value) <= maxLen &&
		utf8.ValidString(value) &&
		!strings.ContainsRune(value, '\x00')
}
