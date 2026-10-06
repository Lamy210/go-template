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
		{raw: "//", want: false},
		{raw: ":4222", want: false},
		{raw: "nats://:4222", want: false},
		{raw: "ws://:8080", want: false},
		{raw: "nats://", want: false},
		{raw: "nats://127.0.0.1:4222,:4333", want: false},
		{raw: "localhost:0", want: false},
		{raw: "nats://localhost:0", want: false},
		{raw: "nats://localhost:65536", want: false},
		{raw: "ws://localhost:65536", want: false},
		{raw: "nats://127.0.0.1:4222,localhost:0", want: false},
		{raw: "nats://127.0.0.1:4222", want: true},
		{raw: "nats://127.0.0.1:4222/", want: true},
		{raw: "localhost", want: true},
		{raw: "localhost:", want: true},
		{raw: "localhost:1", want: true},
		{raw: "localhost:65535", want: true},
		{raw: "[::1]:4222", want: true},
		{raw: "wss://localhost:443/", want: true},
		{raw: " , nats://127.0.0.1:4222, / ", want: true},
	}

	for _, tt := range tests {
		if got := HasExplicitServer(tt.raw); got != tt.want {
			t.Fatalf("HasExplicitServer(%q) = %t, want %t", tt.raw, got, tt.want)
		}
	}
}
