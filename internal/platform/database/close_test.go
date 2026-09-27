package database

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type recordingCloser struct {
	called atomic.Bool
}

func (c *recordingCloser) Close() {
	c.called.Store(true)
}

type blockingCloser struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingCloser() *blockingCloser {
	return &blockingCloser{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (c *blockingCloser) Close() {
	c.once.Do(func() {
		close(c.started)
	})
	<-c.release
}

func TestCloseWithContextCompletesNormally(t *testing.T) {
	t.Parallel()

	resource := &recordingCloser{}
	if err := closeWithContext(context.Background(), resource); err != nil {
		t.Fatalf("closeWithContext() error = %v", err)
	}
	if !resource.called.Load() {
		t.Fatal("Close() was not called")
	}
}

func TestCloseWithContextReturnsOnDeadline(t *testing.T) {
	t.Parallel()

	resource := newBlockingCloser()
	t.Cleanup(func() {
		select {
		case <-resource.release:
		default:
			close(resource.release)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- closeWithContext(ctx, resource)
	}()

	select {
	case <-resource.started:
	case <-time.After(time.Second):
		t.Fatal("Close() did not start")
	}

	select {
	case err := <-errCh:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("closeWithContext() error = %v, want deadline exceeded", err)
		}
	case <-time.After(time.Second):
		t.Fatal("closeWithContext() did not honor context deadline")
	}

	close(resource.release)
}

func TestCloseRejectsNilPool(t *testing.T) {
	t.Parallel()

	if err := Close(context.Background(), nil); err == nil {
		t.Fatal("Close() nil pool error = nil")
	}
}
