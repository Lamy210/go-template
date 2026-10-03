package telemetry

import "testing"

func TestSignalEndpointPreservesEscapedBasePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		base   string
		signal string
		want   string
	}{
		{
			name:   "escaped leading slash stays escaped",
			base:   "https://collector.example.com/%2Fproxy",
			signal: "traces",
			want:   "https://collector.example.com/%2Fproxy/v1/traces",
		},
		{
			name:   "escaped slash inside base path stays escaped",
			base:   "https://collector.example.com/otel%2Fproxy/",
			signal: "metrics",
			want:   "https://collector.example.com/otel%2Fproxy/v1/metrics",
		},
		{
			name:   "escaped trailing slash stays escaped",
			base:   "https://collector.example.com/proxy%2F",
			signal: "traces",
			want:   "https://collector.example.com/proxy%2F/v1/traces",
		},
		{
			name:   "literal trailing slash after escaped slash is trimmed",
			base:   "https://collector.example.com/proxy%2F/",
			signal: "metrics",
			want:   "https://collector.example.com/proxy%2F/v1/metrics",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := signalEndpoint(tt.base, tt.signal)
			if err != nil {
				t.Fatalf("signalEndpoint() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("signalEndpoint() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSignalEndpointRejectsMissingHostname(t *testing.T) {
	t.Parallel()

	for _, endpoint := range []string{
		"http://:4318",
		"https://:443/otel",
	} {
		if _, err := signalEndpoint(endpoint, "traces"); err == nil {
			t.Fatalf("signalEndpoint(%q) error = nil, want missing hostname error", endpoint)
		}
	}
}
