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

func TestClientConfigRejectsInvalidDestinationPort(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{
		"localhost:0",
		"nats://localhost:0",
		"nats://localhost:65536",
		"ws://localhost:65536",
		"nats://127.0.0.1:4222,localhost:0",
	} {
		cfg := testClientURLConfig()
		cfg.URL = raw
		if err := cfg.Validate(); err == nil {
			t.Fatalf("Validate(%q) error = nil, want invalid destination port rejected", raw)
		}
	}
}

func TestClientConfigRejectsMixedWebsocketTransportModes(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{
		"ws://localhost:8080,nats://localhost:4222",
		"nats://localhost:4222,wss://localhost:443",
		"wss://localhost:443,tls://localhost:4443",
		"ws://localhost:8080,tcp://localhost:4222",
	} {
		cfg := testClientURLConfig()
		cfg.URL = raw
		if err := cfg.Validate(); err == nil {
			t.Fatalf("Validate(%q) error = nil, want mixed websocket transport rejected", raw)
		}
	}
}

func TestClientConfigAcceptsURLListWithExplicitServer(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{
		" , nats://127.0.0.1:4222, / ",
		"localhost",
		"localhost:",
		"localhost:1",
		"localhost:65535",
		"[::1]:4222",
		"wss://localhost:443/",
		"ws://localhost:8080,wss://localhost:443",
		"nats://localhost:4222,tls://localhost:4443",
		"tcp://localhost:4222,nats://localhost:4223",
	} {
		cfg := testClientURLConfig()
		cfg.URL = raw
		if err := cfg.Validate(); err != nil {
			t.Fatalf("Validate(%q) error = %v, want explicit server accepted", raw, err)
		}
	}
}
