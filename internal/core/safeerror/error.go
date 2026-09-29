// Package safeerror provides transport-neutral wrappers for internal causes
// whose raw text must not appear in ordinary logs or generic error strings.
package safeerror

type wrapped struct {
	operation string
	cause     error
}

// Error returns only the caller-supplied safe operation name.
func (e *wrapped) Error() string {
	return e.operation
}

// Unwrap preserves the original cause for errors.Is/errors.As traversal.
func (e *wrapped) Unwrap() error {
	return e.cause
}

// Wrap retains cause for programmatic error inspection while exposing only the
// safe operation string through Error. A nil cause produces no error.
func Wrap(operation string, cause error) error {
	if cause == nil {
		return nil
	}
	return &wrapped{
		operation: operation,
		cause:     cause,
	}
}
