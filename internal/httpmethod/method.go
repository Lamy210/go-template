// Package httpmethod centralizes low-cardinality HTTP method policy shared
// by transport logging and telemetry.
package httpmethod

import "net/http"

const Other = "HTTP"

// LowCardinality returns a bounded method label suitable for logs, metrics, and
// span names. Standard net/http methods are preserved; every other token is
// collapsed to Other so attacker-controlled methods cannot create unbounded
// cardinality.
func LowCardinality(method string) string {
	switch method {
	case http.MethodConnect,
		http.MethodDelete,
		http.MethodGet,
		http.MethodHead,
		http.MethodOptions,
		http.MethodPatch,
		http.MethodPost,
		http.MethodPut,
		http.MethodTrace:
		return method
	default:
		return Other
	}
}
