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
