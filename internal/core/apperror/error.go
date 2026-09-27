// Package apperror defines transport-neutral application errors.
package apperror

import "slices"

// Kind classifies an application error by semantics that transports can map to
// their own status model. Kinds must not contain HTTP, gRPC, database, or
// framework-specific values.
type Kind string

const (
	KindInternal          Kind = "internal"
	KindInvalidArgument   Kind = "invalid_argument"
	KindUnauthenticated   Kind = "unauthenticated"
	KindPermissionDenied  Kind = "permission_denied"
	KindNotFound          Kind = "not_found"
	KindConflict          Kind = "conflict"
	KindResourceExhausted Kind = "resource_exhausted"
	KindUnavailable       Kind = "unavailable"
)

// Valid reports whether k is one of the architecture-level semantic kinds
// understood by this version of the core contract.
func (k Kind) Valid() bool {
	switch k {
	case KindInternal,
		KindInvalidArgument,
		KindUnauthenticated,
		KindPermissionDenied,
		KindNotFound,
		KindConflict,
		KindResourceExhausted,
		KindUnavailable:
		return true
	default:
		return false
	}
}

// Code is a stable machine-readable error code.
//
// Prefer module-specific codes such as "user_not_found" over exposing
// implementation details or transport status names.
type Code string

// Detail is safe, client-facing structured information about an error.
// Do not put secrets, raw dependency errors, SQL, tokens, or credentials here.
type Detail struct {
	Field   string
	Code    Code
	Message string
}

// Error is a transport-neutral application error.
//
// publicMessage and details are safe to expose to a client. cause is retained
// only for errors.Is/errors.As traversal and is deliberately excluded from
// Error() so accidental logging does not reveal dependency error strings.
type Error struct {
	kind          Kind
	code          Code
	publicMessage string
	details       []Detail
	cause         error
}

// New creates an application error without an underlying cause.
func New(kind Kind, code Code, publicMessage string, details ...Detail) *Error {
	return &Error{
		kind:          kind,
		code:          code,
		publicMessage: publicMessage,
		details:       slices.Clone(details),
	}
}

// Wrap creates an application error that retains cause for errors.Is/errors.As
// while keeping the cause out of the public error string.
func Wrap(cause error, kind Kind, code Code, publicMessage string, details ...Detail) *Error {
	return &Error{
		kind:          kind,
		code:          code,
		publicMessage: publicMessage,
		details:       slices.Clone(details),
		cause:         cause,
	}
}

// Error returns only safe application-level information.
func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.code == "" {
		if e.publicMessage == "" {
			return "application error"
		}
		return e.publicMessage
	}
	if e.publicMessage == "" {
		return string(e.code)
	}
	return string(e.code) + ": " + e.publicMessage
}

// Unwrap exposes the cause to standard errors.Is/errors.As traversal.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// Kind returns the transport-neutral error classification.
func (e *Error) Kind() Kind {
	if e == nil {
		return KindInternal
	}
	return e.kind
}

// Code returns the stable machine-readable code.
func (e *Error) Code() Code {
	if e == nil {
		return ""
	}
	return e.code
}

// PublicMessage returns the safe client-facing message.
func (e *Error) PublicMessage() string {
	if e == nil {
		return ""
	}
	return e.publicMessage
}

// Details returns a defensive copy of safe client-facing details.
func (e *Error) Details() []Detail {
	if e == nil {
		return nil
	}
	return slices.Clone(e.details)
}
