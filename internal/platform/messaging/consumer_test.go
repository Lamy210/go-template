package messaging

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestInvokeHandlerReturnsHandlerError(t *testing.T) {
	t.Parallel()

	want := errors.New("handler failed")
	got := invokeHandler(
		context.Background(),
		func(context.Context, Message) error {
			return want
		},
		Message{},
	)
	if !errors.Is(got, want) {
		t.Fatalf("invokeHandler() error = %v, want wrapped/identical %v", got, want)
	}
}

func TestInvokeHandlerContainsPanicWithoutExposingValue(t *testing.T) {
	t.Parallel()

	const sensitive = "secret-bearing panic value"
	got := invokeHandler(
		context.Background(),
		func(context.Context, Message) error {
			panic(sensitive)
		},
		Message{},
	)
	if got == nil {
		t.Fatal("invokeHandler() error = nil, want panic conversion")
	}
	if _, ok := got.(handlerPanicError); !ok {
		t.Fatalf("invokeHandler() error type = %T, want handlerPanicError", got)
	}
	if strings.Contains(got.Error(), sensitive) {
		t.Fatalf("panic value leaked through error: %q", got.Error())
	}
}
