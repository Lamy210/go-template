package httpmethod

import (
	"net/http"
	"testing"
)

func TestLowCardinality(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		method string
		want   string
	}{
		{name: "get", method: http.MethodGet, want: http.MethodGet},
		{name: "post", method: http.MethodPost, want: http.MethodPost},
		{name: "connect", method: http.MethodConnect, want: http.MethodConnect},
		{name: "unknown", method: "BREW-tenant-12345", want: Other},
		{name: "empty", method: "", want: Other},
		{name: "lowercase known token", method: "get", want: Other},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := LowCardinality(tt.method); got != tt.want {
				t.Fatalf("LowCardinality(%q) = %q, want %q", tt.method, got, tt.want)
			}
		})
	}
}
