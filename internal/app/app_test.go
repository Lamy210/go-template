package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Lamy210/go-template/internal/buildinfo"
	"github.com/Lamy210/go-template/internal/platform/messaging"
	"github.com/Lamy210/go-template/internal/platform/outbox"
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

func TestClassifyOutboxPublishErrorMarksOnlyDeterministicRejections(t *testing.T) {
	t.Parallel()

	for _, cause := range []error{
		messaging.ErrInvalidPublishSubject,
		messaging.ErrInvalidMessageID,
	} {
		err := classifyOutboxPublishError(cause)
		if !errors.Is(err, outbox.ErrPermanentPublishFailure) {
			t.Fatalf("classified error = %v, want permanent publish failure", err)
		}
		if !errors.Is(err, cause) {
			t.Fatalf("classified error lost cause %v", cause)
		}
	}

	for _, transient := range []error{
		errors.New("temporary broker failure"),
		messaging.ErrMessageTooLarge,
	} {
		if got := classifyOutboxPublishError(transient); got != transient {
			t.Fatalf("transient error was reclassified: %v", got)
		}
	}
	if got := classifyOutboxPublishError(nil); got != nil {
		t.Fatalf("nil error classified as %v", got)
	}
}

func TestPreserveLateServeErrorJoinsConcurrentFailure(t *testing.T) {
	t.Parallel()

	runErr := errors.New("shutdown requested")
	serveErr := errors.New("accept failed")
	errCh := make(chan error, 1)
	errCh <- serveErr

	got := preserveLateServeError(runErr, errCh)
	if !errors.Is(got, runErr) {
		t.Fatalf("preserveLateServeError() lost existing run error: %v", got)
	}
	if !errors.Is(got, serveErr) {
		t.Fatalf("preserveLateServeError() lost late serve error: %v", got)
	}
}

func TestPreserveLateServeErrorLeavesRunErrorWhenChannelEmpty(t *testing.T) {
	t.Parallel()

	runErr := errors.New("shutdown requested")
	errCh := make(chan error, 1)

	if got := preserveLateServeError(runErr, errCh); got != runErr {
		t.Fatalf("preserveLateServeError() = %v, want original run error", got)
	}
}
