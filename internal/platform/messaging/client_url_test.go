package messaging

import (
	"testing"
	"time"
)

func TestClientConfigRejectsURLWithNoExplicitServer(t *testing.T) {
	t.Parallel()

	base := ClientConfig{
		URL:            "nats://127.0.0.1:4222",
		Name:           "test",
		ConnectTimeout: time.Second,
		ReconnectWait:  time.Second,
		MaxReconnects:  1,
		DrainTimeout:   time.Second,
		RequestTimeout: time.Second,
	}

	for _, raw := range []string{",", " , ", "/"} {
		cfg := base
		cfg.URL = raw
		if err := cfg.Validate(); err == nil {
			t.Fatalf("Validate(%q) error = nil, want URL with no explicit server rejected", raw)
		}
	}
}
