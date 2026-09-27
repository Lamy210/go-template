package httpserver

import (
	"context"

	"github.com/danielgtaylor/huma/v2"
)

type operationHandler[I, O any] func(context.Context, *I) (*O, error)

// registerOperation centralizes application-error translation for Huma
// handlers. Transport adapters should register operations through this helper
// instead of returning arbitrary dependency errors directly to Huma.
func registerOperation[I, O any](
	api huma.API,
	operation huma.Operation,
	handler operationHandler[I, O],
) {
	huma.Register(api, operation, func(ctx context.Context, input *I) (*O, error) {
		output, err := handler(ctx, input)
		if err != nil {
			return nil, toHTTPError(ctx, err)
		}
		return output, nil
	})
}
