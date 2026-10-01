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

func TestRunLifecycleOperationsReturnsWhenCallbackIgnoresDeadline(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	finished := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := runLifecycleOperations(
		ctx,
		lifecycleOperation{
			name: "shutdown stuck telemetry signal",
			run: func(context.Context) error {
				defer close(finished)
				<-release
				return nil
			},
		},
	)
	elapsed := time.Since(start)
	close(release)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("runLifecycleOperations() error = %v, want deadline exceeded", err)
	}
	if got := err.Error(); got != "wait for telemetry lifecycle operations" {
		t.Fatalf("runLifecycleOperations() error text = %q", got)
	}
	if elapsed >= time.Second {
		t.Fatalf("runLifecycleOperations() took %v, want bounded deadline return", elapsed)
	}

	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("released lifecycle callback did not finish")
	}
}

func TestRunLifecycleOperationsContainsPanicsAndFinishesSiblings(t *testing.T) {
	t.Parallel()

	const sensitive = "sensitive telemetry lifecycle panic"
	siblingFinished := make(chan struct{})

	err := runLifecycleOperations(
		context.Background(),
		lifecycleOperation{
			name: "shutdown telemetry traces",
			run: func(context.Context) error {
				panic(sensitive)
			},
		},
		lifecycleOperation{
			name: "shutdown telemetry metrics",
			run: func(context.Context) error {
				close(siblingFinished)
				return nil
			},
		},
	)
	if err == nil {
		t.Fatal("runLifecycleOperations() error = nil, want panic error")
	}
	if !errors.Is(err, errLifecycleOperationPanic) {
		t.Fatalf("runLifecycleOperations() error = %v, want panic sentinel", err)
	}
	if strings.Contains(err.Error(), sensitive) {
		t.Fatalf("lifecycle error exposed panic value: %q", err.Error())
	}
	if err.Error() != "shutdown telemetry traces" {
		t.Fatalf("lifecycle error = %q, want sanitized operation name", err.Error())
	}

	select {
	case <-siblingFinished:
	default:
		t.Fatal("sibling lifecycle operation did not finish")
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
	const want = "flush telemetry traces\nflush telemetry metrics"
	if err.Error() != want {
		t.Fatalf("lifecycle error = %q, want deterministic %q", err.Error(), want)
	}
}
