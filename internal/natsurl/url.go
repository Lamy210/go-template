// Package natsurl contains connection-URL policy shared by process config and
// the reusable NATS adapter.
package natsurl

import "strings"

// HasExplicitServer reports whether raw contains at least one server entry that
// survives the normalization used by pinned nats.go v1.54.0 before building its
// connection pool.
//
// nats.go splits on commas, trims surrounding whitespace, removes one trailing
// slash, and drops empty entries. If no entry survives, the client inserts its
// DefaultURL. Requiring one surviving entry prevents malformed configuration
// from silently falling back to that implicit server.
func HasExplicitServer(raw string) bool {
	for _, entry := range strings.Split(raw, ",") {
		if strings.TrimSuffix(strings.TrimSpace(entry), "/") != "" {
			return true
		}
	}
	return false
}
