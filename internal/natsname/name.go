// Package natsname validates JetStream stream and consumer identifiers.
package natsname

import (
	"errors"
	"strings"
)

const maxNameBytes = 255

var errInvalidName = errors.New("invalid NATS JetStream name")

// Validate applies the JetStream stream/consumer name grammar used by the
// pinned NATS server. Names must contain 1-255 UTF-8 bytes and may not contain
// wildcards, dots, spaces, path separators, backslashes, tabs, form feeds,
// carriage returns, or newlines.
func Validate(name string) error {
	if name == "" ||
		len(name) > maxNameBytes ||
		strings.ContainsAny(name, ">*. /\\\t\f\r\n") {
		return errInvalidName
	}
	return nil
}
