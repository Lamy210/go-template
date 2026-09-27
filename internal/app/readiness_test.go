package app

import (
	"context"
	"errors"
	"testing"

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

	calls := 0
	check := combineReadiness(
		func(context.Context) error {
			calls++
			return nil
		},
		nil,
		func(context.Context) error {
			calls++
			return nil
		},
	)
	if check == nil {
		t.Fatal("combineReadiness() returned nil")
	}

	if err := check(context.Background()); err != nil {
		t.Fatalf("combined readiness error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("readiness calls = %d, want 2", calls)
	}
}

func TestCombineReadinessStopsAtFirstFailure(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("dependency unavailable")
	secondCalled := false
	check := combineReadiness(
		httpserver.ReadinessCheck(func(context.Context) error {
			return sentinel
		}),
		httpserver.ReadinessCheck(func(context.Context) error {
			secondCalled = true
			return nil
		}),
	)

	err := check(context.Background())
	if !errors.Is(err, sentinel) {
		t.Fatalf("combined readiness error = %v, want sentinel", err)
	}
	if secondCalled {
		t.Fatal("readiness continued after first failure")
	}
}
