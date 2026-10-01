// Package messageid validates stable opaque message identifiers used across
// durable text storage and message-header boundaries.
package messageid

import (
	"errors"
	"net/textproto"
	"strings"
	"unicode/utf8"
)

var errInvalid = errors.New("message ID must be canonical text")

// Validate rejects identifiers that are not safe canonical text across the
// supported durable-storage and message-header boundaries.
//
// Go/NATS header writers trim leading/trailing ASCII whitespace and replace CR
// or LF characters. PostgreSQL text cannot store NUL. Rejecting those forms
// preserves stable-ID semantics while otherwise allowing arbitrary UTF-8 text.
func Validate(id string) error {
	if id == "" || !utf8.ValidString(id) {
		return errInvalid
	}
	if textproto.TrimString(id) != id || strings.ContainsAny(id, "\x00\r\n") {
		return errInvalid
	}
	return nil
}
