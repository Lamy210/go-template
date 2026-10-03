package listenaddr

import "testing"

func TestValidateTCPRejectsInvalidNumericPortRange(t *testing.T) {
	t.Parallel()

	for _, address := range []string{
		":-1",
		":65536",
		"127.0.0.1:99999",
	} {
		t.Run(address, func(t *testing.T) {
			t.Parallel()

			if err := ValidateTCP(address); err == nil {
				t.Fatalf("ValidateTCP(%q) error = nil, want invalid port error", address)
			}
		})
	}
}

func TestValidateTCPAllowsSupportedPortForms(t *testing.T) {
	t.Parallel()

	for _, address := range []string{
		":",
		":0",
		":65535",
		"localhost:http",
	} {
		t.Run(address, func(t *testing.T) {
			t.Parallel()

			if err := ValidateTCP(address); err != nil {
				t.Fatalf("ValidateTCP(%q) error = %v", address, err)
			}
		})
	}
}
