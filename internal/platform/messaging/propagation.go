package messaging

import (
	"sort"
	"strings"

	coreprop "github.com/Lamy210/go-template/internal/core/propagation"
	"github.com/nats-io/nats.go"
)

type natsHeaderCarrier struct {
	header nats.Header
}

var _ coreprop.TextMapCarrier = natsHeaderCarrier{}

func (c natsHeaderCarrier) Get(key string) string {
	if value := c.header.Get(key); value != "" {
		return value
	}
	for existingKey, values := range c.header {
		if !strings.EqualFold(existingKey, key) || len(values) == 0 {
			continue
		}
		return values[0]
	}
	return ""
}

func (c natsHeaderCarrier) Set(key, value string) {
	for existingKey := range c.header {
		if strings.EqualFold(existingKey, key) {
			c.header.Set(existingKey, value)
			return
		}
	}
	c.header.Set(key, value)
}

func (c natsHeaderCarrier) Keys() []string {
	keys := make([]string, 0, len(c.header))
	for key := range c.header {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// validNATSHeaderKey mirrors the pinned nats.go ADR-4 header-key contract:
// non-empty printable ASCII, excluding colon.
func validNATSHeaderKey(key string) bool {
	if key == "" {
		return false
	}
	for i := 0; i < len(key); i++ {
		if key[i] < 0x21 || key[i] > 0x7e || key[i] == ':' {
			return false
		}
	}
	return true
}
