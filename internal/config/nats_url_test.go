package config

import "testing"

func TestNATSValidateRejectsURLWithNoExplicitServer(t *testing.T) {
	t.Parallel()

	base := defaultNATSConfig()
	base.Enabled = true

	for _, raw := range []string{",", " , ", "/"} {
		cfg := base
		cfg.URL = raw
		if err := cfg.Validate(); err == nil {
			t.Fatalf("Validate(%q) error = nil, want URL with no explicit server rejected", raw)
		}
	}
}

func TestNATSValidateRejectsServerWithoutExplicitHostname(t *testing.T) {
	t.Parallel()

	base := defaultNATSConfig()
	base.Enabled = true

	for _, raw := range []string{
		":4222",
		"nats://:4222",
		"ws://:8080",
		"nats://",
		"nats://127.0.0.1:4222,:4333",
	} {
		cfg := base
		cfg.URL = raw
		if err := cfg.Validate(); err == nil {
			t.Fatalf("Validate(%q) error = nil, want hostname-less server rejected", raw)
		}
	}
}

func TestNATSValidateRejectsInvalidDestinationPort(t *testing.T) {
	t.Parallel()

	base := defaultNATSConfig()
	base.Enabled = true

	for _, raw := range []string{
		"localhost:0",
		"nats://localhost:0",
		"nats://localhost:65536",
		"ws://localhost:65536",
		"nats://127.0.0.1:4222,localhost:0",
	} {
		cfg := base
		cfg.URL = raw
		if err := cfg.Validate(); err == nil {
			t.Fatalf("Validate(%q) error = nil, want invalid destination port rejected", raw)
		}
	}
}

func TestNATSValidateAcceptsExplicitHostnames(t *testing.T) {
	t.Parallel()

	base := defaultNATSConfig()
	base.Enabled = true

	for _, raw := range []string{
		"localhost",
		"localhost:",
		"localhost:1",
		"localhost:65535",
		"nats://localhost:4222",
		"[::1]:4222",
		"wss://localhost:443/",
		" , nats://127.0.0.1:4222, / ",
	} {
		cfg := base
		cfg.URL = raw
		if err := cfg.Validate(); err != nil {
			t.Fatalf("Validate(%q) error = %v, want explicit hostname accepted", raw, err)
		}
	}
}
