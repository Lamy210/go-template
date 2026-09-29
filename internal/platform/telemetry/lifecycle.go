package telemetry

import (
	"context"
	"errors"
)

type lifecycleOperation struct {
	name string
	run  func(context.Context) error
}

type lifecycleResult struct {
	index int
	err   error
}

func runLifecycleOperations(
	ctx context.Context,
	operations ...lifecycleOperation,
) error {
	if len(operations) == 0 {
		return nil
	}

	results := make(chan lifecycleResult, len(operations))
	for index, operation := range operations {
		go func(index int, operation lifecycleOperation) {
			results <- lifecycleResult{
				index: index,
				err: newOperationError(
					operation.name,
					operation.run(ctx),
				),
			}
		}(index, operation)
	}

	errs := make([]error, len(operations))
	for range operations {
		result := <-results
		errs[result.index] = result.err
	}
	return errors.Join(errs...)
}
