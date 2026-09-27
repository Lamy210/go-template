package httpserver

import (
	"context"

	"github.com/danielgtaylor/huma/v2"
)

type operationHandler[I, O any] func(context.Context, *I) (*O, error)

// registerOperation centralizes application-error translation and request-body
// limits for Huma handlers. HTTP_MAX_BODY_BYTES is the process-wide hard cap;
// an operation may request a smaller positive limit but never a larger or
// unlimited one.
func registerOperation[I, O any](
	api huma.API,
	maxBodyBytes int64,
	operation huma.Operation,
	handler operationHandler[I, O],
) {
	operation.MaxBodyBytes = boundedOperationBodyLimit(
		operation.MaxBodyBytes,
		maxBodyBytes,
	)

	huma.Register(api, operation, func(ctx context.Context, input *I) (*O, error) {
		output, err := handler(ctx, input)
		if err != nil {
			return nil, toHTTPError(ctx, err)
		}
		return output, nil
	})
}

func boundedOperationBodyLimit(operationLimit, globalLimit int64) int64 {
	if operationLimit <= 0 || operationLimit > globalLimit {
		return globalLimit
	}
	return operationLimit
}
