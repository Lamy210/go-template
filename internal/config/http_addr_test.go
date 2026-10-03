package config

import "testing"

func TestValidateRejectsUnusableHTTPListenAddresses(t *testing.T) {
	t.Parallel()

	base, err := load(func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("load defaults: %v", err)
	}

	for _, addr := range []string{
		"localhost",
		":",
		" 127.0.0.1:8080 ",
	} {
		addr := addr
		t.Run(addr, func(t *testing.T) {
			t.Parallel()

			cfg := base
			cfg.HTTP.Addr = addr
			if err := cfg.Validate(); err == nil {
				t.Fatalf("Validate() HTTP_ADDR %q error = nil, want error", addr)
			}
		})
	}
}
