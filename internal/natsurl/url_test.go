package natsurl

import "testing"

func TestHasExplicitServerMatchesPinnedNormalization(t *testing.T) {
	t.Parallel()

	tests := []struct {
		raw  string
		want bool
	}{
		{raw: "", want: false},
		{raw: "   ", want: false},
		{raw: ",", want: false},
		{raw: " , ", want: false},
		{raw: "/", want: false},
		{raw: " , /, ", want: false},
		{raw: "nats://127.0.0.1:4222", want: true},
		{raw: "nats://127.0.0.1:4222/", want: true},
		{raw: " , nats://127.0.0.1:4222, / ", want: true},
		{raw: "//", want: true},
	}

	for _, tt := range tests {
		if got := HasExplicitServer(tt.raw); got != tt.want {
			t.Fatalf("HasExplicitServer(%q) = %t, want %t", tt.raw, got, tt.want)
		}
	}
}
