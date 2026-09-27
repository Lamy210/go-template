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
