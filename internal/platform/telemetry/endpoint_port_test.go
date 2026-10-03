package telemetry

import "testing"

func TestConfigRejectsOutOfRangeEndpointPorts(t *testing.T) {
	t.Parallel()

	for _, endpoint := range []string{
		"http://collector.example.com:0",
		"http://collector.example.com:65536",
	} {
		cfg := testConfig()
		cfg.Endpoint = endpoint
		if err := cfg.Validate(); err == nil {
			t.Fatalf("Validate(%q) error = nil, want invalid port error", endpoint)
		}
	}
}

func TestConfigAcceptsValidEndpointPortBoundary(t *testing.T) {
	t.Parallel()

	for _, endpoint := range []string{
		"http://collector.example.com:1",
		"http://collector.example.com:65535",
	} {
		cfg := testConfig()
		cfg.Endpoint = endpoint
		if err := cfg.Validate(); err != nil {
			t.Fatalf("Validate(%q) error = %v", endpoint, err)
		}
	}
}
