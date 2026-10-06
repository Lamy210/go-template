package messaging

import (
	"testing"
	"time"
)

func testClientURLConfig() ClientConfig {
	return ClientConfig{
		URL:            "nats://127.0.0.1:4222",
		Name:           "test",
		ConnectTimeout: time.Second,
		ReconnectWait:  time.Second,
		MaxReconnects:  1,
		DrainTimeout:   time.Second,
		RequestTimeout: time.Second,
	}
}

func TestClientConfigRejectsURLWithNoExplicitServer(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{",", " , ", "/"} {
		cfg := testClientURLConfig()
		cfg.URL = raw
		if err := cfg.Validate(); err == nil {
			t.Fatalf("Validate(%q) error = nil, want URL with no explicit server rejected", raw)
		}
	}
}

func TestClientConfigRejectsServerWithoutExplicitHostname(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{
		":4222",
		"nats://:4222",
		"ws://:8080",
		"nats://",
		"nats://127.0.0.1:4222,:4333",
	} {
		cfg := testClientURLConfig()
		cfg.URL = raw
		if err := cfg.Validate(); err == nil {
			t.Fatalf("Validate(%q) error = nil, want hostname-less server rejected", raw)
		}
	}
}

func TestClientConfigAcceptsURLListWithExplicitServer(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{
		" , nats://127.0.0.1:4222, / ",
		"localhost:4222",
		"[::1]:4222",
		"wss://localhost:443/",
	} {
		cfg := testClientURLConfig()
		cfg.URL = raw
		if err := cfg.Validate(); err != nil {
			t.Fatalf("Validate(%q) error = %v, want explicit server accepted", raw, err)
		}
	}
}
