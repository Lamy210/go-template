// Package propagation defines transport-neutral text-map context propagation contracts.
package propagation

import "context"

// TextMapCarrier is the minimal key/value carrier used for cross-process context
// propagation. Transport adapters provide concrete implementations for their headers.
type TextMapCarrier interface {
	Get(key string) string
	Set(key, value string)
	Keys() []string
}

// TextMapPropagator injects and extracts cross-process context without making
// transport packages depend on a specific observability implementation.
type TextMapPropagator interface {
	Inject(context.Context, TextMapCarrier)
	Extract(context.Context, TextMapCarrier) context.Context
}
