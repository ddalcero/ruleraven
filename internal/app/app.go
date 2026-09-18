package app

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Runner is a long-lived application component. It must return when ctx is
// cancelled.
type Runner interface {
	Run(context.Context) error
}

// Closer releases a resource after all runners have stopped.
type Closer interface {
	Close(context.Context) error
}

type Config struct {
	Runners         []Runner
	Closers         []Closer
	ShutdownTimeout time.Duration
}

type App struct {
	runners         []Runner
	closers         []Closer
	shutdownTimeout time.Duration
}

func New(config Config) (*App, error) {
	if config.ShutdownTimeout <= 0 {
		return nil, fmt.Errorf("application shutdown timeout must be positive")
	}
	for _, runner := range config.Runners {
		if runner == nil {
			return nil, fmt.Errorf("application runner must not be nil")
		}
	}
	for _, closer := range config.Closers {
		if closer == nil {
			return nil, fmt.Errorf("application closer must not be nil")
		}
	}
	return &App{
		runners:         append([]Runner(nil), config.Runners...),
		closers:         append([]Closer(nil), config.Closers...),
		shutdownTimeout: config.ShutdownTimeout,
	}, nil
}

// Run starts all components, cancels siblings when one exits, waits for a
// bounded graceful shutdown, and then closes resources in reverse order.
func (a *App) Run(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	results := make(chan error, len(a.runners))
	for _, runner := range a.runners {
		go func(r Runner) { results <- r.Run(runCtx) }(runner)
	}

	var runErr error
	remaining := len(a.runners)
	if remaining > 0 {
		select {
		case err := <-results:
			remaining--
			runErr = normalizeRunnerError(ctx, err)
		case <-ctx.Done():
		}
	}
	cancel()

	timer := time.NewTimer(a.shutdownTimeout)
	defer timer.Stop()
	for remaining > 0 {
		select {
		case err := <-results:
			remaining--
			if runErr == nil {
				runErr = normalizeRunnerError(ctx, err)
			}
		case <-timer.C:
			runErr = errors.Join(runErr, fmt.Errorf("application runners did not stop within %s", a.shutdownTimeout))
			remaining = 0
		}
	}

	closeCtx, closeCancel := context.WithTimeout(context.Background(), a.shutdownTimeout)
	defer closeCancel()
	for index := len(a.closers) - 1; index >= 0; index-- {
		if err := a.closers[index].Close(closeCtx); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("close application resource: %w", err))
		}
	}
	return runErr
}

func normalizeRunnerError(parent context.Context, err error) error {
	if err == nil || parent.Err() != nil && errors.Is(err, parent.Err()) || errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

// PeriodicRunner repeatedly drains a finite runner until shutdown.
type PeriodicRunner struct {
	Runner   Runner
	Interval time.Duration
}

func (r PeriodicRunner) Run(ctx context.Context) error {
	if r.Runner == nil || r.Interval <= 0 {
		return fmt.Errorf("periodic runner and positive interval are required")
	}
	for {
		if err := r.Runner.Run(ctx); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		timer := time.NewTimer(r.Interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil
		case <-timer.C:
		}
	}
}

// FuncRunner adapts a function to Runner.
type FuncRunner func(context.Context) error

func (f FuncRunner) Run(ctx context.Context) error { return f(ctx) }
