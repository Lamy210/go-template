package app

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/Lamy210/go-template/internal/buildinfo"
)

func TestNewLoggerAddsCommonFields(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	logger, err := newLogger(
		&out,
		"INFO",
		"example-api",
		"test",
		buildinfo.Info{Version: "1.2.3", Commit: "abc123", BuildDate: "unknown"},
	)
	if err != nil {
		t.Fatalf("newLogger() error = %v", err)
	}

	logger.Info("hello")

	var record map[string]any
	if err := json.Unmarshal(out.Bytes(), &record); err != nil {
		t.Fatalf("decode log record: %v; log=%s", err, out.String())
	}

	for key, want := range map[string]string{
		"service":     "example-api",
		"version":     "1.2.3",
		"environment": "test",
	} {
		if got, ok := record[key].(string); !ok || got != want {
			t.Fatalf("%s = %#v, want %q", key, record[key], want)
		}
	}
}

func TestNewLoggerRejectsInvalidLevel(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	_, err := newLogger(&out, "not-a-level", "example-api", "test", buildinfo.Info{})
	if err == nil {
		t.Fatal("newLogger() error = nil, want error")
	}
}
