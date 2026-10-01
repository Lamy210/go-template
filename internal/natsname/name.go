// Package natsname validates JetStream stream and consumer identifiers.
package natsname

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxNameBytes = 255

var errInvalidName = errors.New("invalid NATS JetStream name")

// Validate applies the JetStream stream/consumer name constraints used by the
// pinned NATS profile. Names must contain 1-255 valid UTF-8 bytes and may not
// contain wildcards, dots, path separators, whitespace, or non-printable
// characters.
func Validate(name string) error {
	if name == "" ||
		!utf8.ValidString(name) ||
		len(name) > maxNameBytes ||
		strings.ContainsAny(name, ">*./\\") {
		return errInvalidName
	}
	for _, r := range name {
		if unicode.IsSpace(r) || !unicode.IsPrint(r) {
			return errInvalidName
		}
	}
	return nil
}
