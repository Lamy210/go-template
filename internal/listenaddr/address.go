// Package listenaddr validates listener address syntax shared by process
// configuration and transport adapters.
package listenaddr

import (
	"errors"
	"net"
	"strings"
)

// ValidateTCP rejects address text that cannot be interpreted as a stable TCP
// host:port pair. An empty host and an empty service are valid: net.Listen
// treats them as a wildcard host and ephemeral port respectively.
func ValidateTCP(address string) error {
	if strings.TrimSpace(address) == "" {
		return errors.New("listen address must not be empty")
	}
	if strings.TrimSpace(address) != address {
		return errors.New("listen address must not contain surrounding whitespace")
	}
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return errors.New("listen address must use host:port form")
	}
	if _, err := net.LookupPort("tcp", port); err != nil {
		return errors.New("listen address contains an invalid TCP port")
	}
	return nil
}
