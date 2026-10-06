// Package natsurl contains connection-URL policy shared by process config and
// the reusable NATS adapter.
package natsurl

import (
	"net/url"
	"strconv"
	"strings"
)

// HasExplicitServer reports whether raw contains at least one server entry and
// every surviving entry matches the connection-pool constraints enforced by
// pinned nats.go v1.54.0 before network I/O.
//
// nats.go splits on commas, trims surrounding whitespace, removes one trailing
// slash, and drops empty entries. If no entry survives, the client inserts its
// DefaultURL. It also accepts an empty TCP host such as ":4222", which Go dials
// as the local machine. Requiring a hostname prevents both implicit fallback
// behaviors when configuration accidentally omits the intended server. Explicit
// destination ports must also fit the TCP/UDP port range and be non-zero;
// omitted ports remain valid because nats.go supplies its scheme-specific
// default. Finally, websocket and non-websocket entries cannot be mixed because
// nats.go rejects that pool synchronously.
func HasExplicitServer(raw string) bool {
	found := false
	websocketModeSet := false
	websocketMode := false
	for _, entry := range strings.Split(raw, ",") {
		trimmed := strings.TrimSpace(entry)
		normalized := strings.TrimSuffix(trimmed, "/")
		if normalized == "" {
			continue
		}
		found = true

		var candidate string
		if strings.Contains(trimmed, "://") {
			// Parse the pre-normalized form so a scheme-only value such as
			// "nats://" cannot lose one slash and be reinterpreted as the bare
			// host "nats" by the pinned client's normalization sequence.
			candidate = trimmed
		} else {
			candidate = "nats://" + normalized
		}

		parsed, err := url.Parse(candidate)
		if err != nil || parsed.Hostname() == "" {
			return false
		}
		if port := parsed.Port(); port != "" {
			value, err := strconv.Atoi(port)
			if err != nil || value < 1 || value > 65535 {
				return false
			}
		}

		isWebsocket := parsed.Scheme == "ws" || parsed.Scheme == "wss"
		if !websocketModeSet {
			websocketMode = isWebsocket
			websocketModeSet = true
		} else if isWebsocket != websocketMode {
			return false
		}
	}
	return found
}
