// Package messageid validates stable opaque message identifiers that must
// survive text-header serialization without normalization.
package messageid

import (
	"errors"
	"net/textproto"
	"strings"
)

var errInvalid = errors.New("message ID must be canonical text")

// Validate rejects identifiers whose serialized header value would differ from
// the original application value.
//
// Go/NATS header writers trim leading/trailing ASCII whitespace and replace CR
// or LF characters. Rejecting those forms preserves exact stable-ID semantics
// for deduplication keys while otherwise allowing arbitrary UTF-8 text.
func Validate(id string) error {
	if id == "" {
		return errInvalid
	}
	if textproto.TrimString(id) != id || strings.ContainsAny(id, "\r\n") {
		return errInvalid
	}
	return nil
}
