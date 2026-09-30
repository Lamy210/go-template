package app

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Lamy210/go-template/internal/platform/httpserver"
)

func TestCombineReadinessWithoutChecksReturnsNil(t *testing.T) {
	t.Parallel()

	if got := combineReadiness(nil); got != nil {
		t.Fatal("combineReadiness(nil) returned a check, want nil")
	}
}

func TestCombineReadinessRunsAllChecks(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	check := combineReadiness(
		func(context.Context) error {
			calls.Add(1)
			return nil
		},
		nil,
		func(context.Context) error {
			calls.Add(1)
			return nil
		},
	)
	if check == nil {
		t.Fatal("combineReadiness() returned nil")
	}

	if err := check(context.Background()); err != nil {
		t.Fatalf("combined readiness error = %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("readiness calls = %d, want 2", got)
	}
}

func TestCombineReadinessRunsChecksConcurrently(t *testing.T) {
	t.Parallel()

	started := make(chan struct{}, 2)
	release := make(chan struct{})
	check := combineReadiness(
		func(context.Context) error {
			started <- struct{}{}
			<-release
			return nil
		},
		func(context.Context) error {
			started <- struct{}{}
			<-release
			return nil
		},
	)

	done := make(chan error, 1)
	go func() {
		done <- check(context.Background())
	}()

	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("readiness checks did not start concurrently")
		}
	}
	close(release)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("combined readiness error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("combined readiness did not finish")
	}
}

func TestCombineReadinessContainsPanics(t *testing.T) {
	t.Parallel()

	const sensitive = "sensitive readiness panic"
	check := combineReadiness(
		func(context.Context) error {
			panic(sensitive)
		},
	)
	if check == nil {
		t.Fatal("combineReadiness() returned nil")
	}

	err := check(context.Background())
	if !errors.Is(err, errReadinessCheckPanic) {
		t.Fatalf("combined readiness error = %v, want panic sentinel", err)
	}
	if strings.Contains(err.Error(), sensitive) {
		t.Fatalf("combined readiness exposed panic value: %q", err.Error())
	}
}

func TestCombineReadinessReturnsOnParentCancellation(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	checkCanceled := make(chan struct{})
	check := combineReadiness(
		func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			close(checkCanceled)
			return ctx.Err()
		},
	)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- check(ctx)
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("readiness check did not start")
	}
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("combined readiness error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("combined readiness ignored parent cancellation")
	}

	select {
	case <-checkCanceled:
	case <-time.After(time.Second):
		t.Fatal("readiness check did not observe parent cancellation")
	}
}

func TestCombineReadinessCancelsSiblingsAfterFailure(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("dependency unavailable")
	started := make(chan struct{})
	canceled := make(chan struct{})

	check := combineReadiness(
		httpserver.ReadinessCheck(func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			close(canceled)
			return ctx.Err()
		}),
		httpserver.ReadinessCheck(func(context.Context) error {
			<-started
			return sentinel
		}),
	)

	err := check(context.Background())
	if !errors.Is(err, sentinel) {
		t.Fatalf("combined readiness error = %v, want sentinel", err)
	}

	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("sibling readiness check was not canceled")
	}
}
