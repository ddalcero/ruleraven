package app_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ddalcero/ruleraven/internal/app"
)

type blockingRunner struct{ stopped chan struct{} }

func (r *blockingRunner) Run(ctx context.Context) error {
	<-ctx.Done()
	close(r.stopped)
	return nil
}

type failingRunner struct{ err error }

func (r failingRunner) Run(context.Context) error { return r.err }

type closeSpy struct{ calls atomic.Int32 }

func (c *closeSpy) Close(context.Context) error {
	c.calls.Add(1)
	return nil
}

func TestAppCancelsSiblingsAndClosesResources(t *testing.T) {
	failure := errors.New("controller failed")
	blocking := &blockingRunner{stopped: make(chan struct{})}
	closer := &closeSpy{}
	application, err := app.New(app.Config{
		Runners: []app.Runner{blocking, failingRunner{err: failure}},
		Closers: []app.Closer{closer}, ShutdownTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	err = application.Run(context.Background())
	if !errors.Is(err, failure) {
		t.Fatalf("Run error = %v, want %v", err, failure)
	}
	select {
	case <-blocking.stopped:
	default:
		t.Fatal("sibling runner was not stopped")
	}
	if got := closer.calls.Load(); got != 1 {
		t.Fatalf("Close calls = %d, want 1", got)
	}
}

func TestAppReturnsCleanlyOnShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	blocking := &blockingRunner{stopped: make(chan struct{})}
	application, err := app.New(app.Config{Runners: []app.Runner{blocking}, ShutdownTimeout: time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cancel()
	if err := application.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
}
