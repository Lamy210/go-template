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

func TestClientConfigAcceptsURLListWithExplicitServer(t *testing.T) {
	t.Parallel()

	cfg := testClientURLConfig()
	cfg.URL = " , nats://127.0.0.1:4222, / "
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want explicit server accepted", err)
	}
}
