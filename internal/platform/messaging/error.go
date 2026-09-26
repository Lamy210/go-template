package messaging

// operationError retains a dependency cause for errors.Is/errors.As traversal
// while keeping raw broker diagnostics out of ordinary logs.
type operationError struct {
	operation string
	cause     error
}

func (e *operationError) Error() string {
	return e.operation
}

func (e *operationError) Unwrap() error {
	return e.cause
}

func newOperationError(operation string, cause error) error {
	if cause == nil {
		return nil
	}
	return &operationError{operation: operation, cause: cause}
}
