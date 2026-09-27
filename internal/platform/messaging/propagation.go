package messaging

import (
	"sort"

	coreprop "github.com/Lamy210/go-template/internal/core/propagation"
	"github.com/nats-io/nats.go"
)

type natsHeaderCarrier struct {
	header nats.Header
}

var _ coreprop.TextMapCarrier = natsHeaderCarrier{}

func (c natsHeaderCarrier) Get(key string) string {
	return c.header.Get(key)
}

func (c natsHeaderCarrier) Set(key, value string) {
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
