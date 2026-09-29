package telemetry

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRunLifecycleOperationsStartsConcurrently(t *testing.T) {
	t.Parallel()

	started := make(chan struct{}, 2)
	release := make(chan struct{})
	done := make(chan error, 1)

	go func() {
		done <- runLifecycleOperations(
			context.Background(),
			lifecycleOperation{
				name: "first",
				run: func(context.Context) error {
					started <- struct{}{}
					<-release
					return nil
				},
			},
			lifecycleOperation{
				name: "second",
				run: func(context.Context) error {
					started <- struct{}{}
					<-release
					return nil
				},
			},
		)
	}()

	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			close(release)
			t.Fatal("lifecycle operations did not start concurrently")
		}
	}
	close(release)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runLifecycleOperations() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runLifecycleOperations() did not complete")
	}
}

func TestRunLifecycleOperationsSanitizesErrorsAndPreservesCauses(t *testing.T) {
	t.Parallel()

	traceCause := errors.New("collector https://secret.example.invalid trace failure")
	metricCause := errors.New("backend token=secret metric failure")

	err := runLifecycleOperations(
		context.Background(),
		lifecycleOperation{
			name: "flush telemetry traces",
			run: func(context.Context) error {
				return traceCause
			},
		},
		lifecycleOperation{
			name: "flush telemetry metrics",
			run: func(context.Context) error {
				return metricCause
			},
		},
	)
	if err == nil {
		t.Fatal("runLifecycleOperations() error = nil, want error")
	}
	if !errors.Is(err, traceCause) || !errors.Is(err, metricCause) {
		t.Fatalf("joined lifecycle error does not retain both causes: %v", err)
	}
	if strings.Contains(err.Error(), "secret.example.invalid") ||
		strings.Contains(err.Error(), "token=secret") {
		t.Fatalf("lifecycle error exposed raw SDK diagnostics: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "flush telemetry traces") ||
		!strings.Contains(err.Error(), "flush telemetry metrics") {
		t.Fatalf("lifecycle error omitted safe operation names: %q", err.Error())
	}
}
