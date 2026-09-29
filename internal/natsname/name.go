// Package natsname validates JetStream stream and consumer identifiers.
package natsname

import (
	"errors"
	"strings"
)

var errInvalidName = errors.New("invalid NATS JetStream name")

// Validate applies the JetStream stream/consumer name grammar used by nats.go.
// Names must be non-empty and may not contain wildcards, dots, spaces,
// path separators, backslashes, tabs, carriage returns, or newlines.
func Validate(name string) error {
	if name == "" || strings.ContainsAny(name, ">*. /\\\t\r\n") {
		return errInvalidName
	}
	return nil
}
