package telemetry

import (
	"context"
	"errors"
)

type lifecycleOperation struct {
	name string
	run  func(context.Context) error
}

func runLifecycleOperations(
	ctx context.Context,
	operations ...lifecycleOperation,
) error {
	if len(operations) == 0 {
		return nil
	}

	results := make(chan error, len(operations))
	for _, operation := range operations {
		go func(operation lifecycleOperation) {
			err := operation.run(ctx)
			results <- newOperationError(operation.name, err)
		}(operation)
	}

	var joined error
	for range operations {
		joined = errors.Join(joined, <-results)
	}
	return joined
}
