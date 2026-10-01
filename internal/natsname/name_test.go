package natsname

import (
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	t.Parallel()

	valid := []string{
		"APP_EVENTS",
		"app-worker",
		"consumer_01",
		"東京-worker",
		"worker-雪",
		strings.Repeat("a", maxNameBytes),
	}
	for _, name := range valid {
		if err := Validate(name); err != nil {
			t.Fatalf("Validate(%q) error = %v", name, err)
		}
	}

	invalid := []string{
		"",
		"APP.EVENTS",
		"app*worker",
		"app>worker",
		"app worker",
		"app/worker",
		"app\\worker",
		"app\tworker",
		"app\fworker",
		"app\rworker",
		"app\nworker",
		"app\vworker",
		"app\x00worker",
		"app\x01worker",
		"app\x7fworker",
		"app\u00a0worker",
		"app\u200bworker",
		string([]byte{0xff}),
		"worker-" + string([]byte{0xc3, 0x28}),
		strings.Repeat("a", maxNameBytes+1),
		strings.Repeat("界", 86), // 258 UTF-8 bytes.
	}
	for _, name := range invalid {
		if err := Validate(name); err == nil {
			t.Fatalf("Validate(%q) error = nil, want error", name)
		}
	}
}
