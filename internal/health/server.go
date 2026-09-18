package health

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"time"
)

const allStartupChecks = configValidated | mongoReady | informersSynced

const (
	configValidated uint32 = 1 << iota
	mongoReady
	informersSynced
)

// Readiness tracks only one-way startup prerequisites. Runtime provider and
// notification availability intentionally cannot alter it.
type Readiness struct{ checks atomic.Uint32 }

func NewReadiness() *Readiness { return &Readiness{} }

func (r *Readiness) MarkConfigValidated() { r.mark(configValidated) }
func (r *Readiness) MarkMongoReady()      { r.mark(mongoReady) }
func (r *Readiness) MarkInformersSynced() { r.mark(informersSynced) }
func (r *Readiness) Ready() bool {
	return r != nil && r.checks.Load()&allStartupChecks == allStartupChecks
}
func (r *Readiness) mark(check uint32) {
	for {
		current := r.checks.Load()
		if r.checks.CompareAndSwap(current, current|check) {
			return
		}
	}
}

type handlerOptions struct{ readiness *Readiness }
type HandlerOption func(*handlerOptions)

func WithReadiness(readiness *Readiness) HandlerOption {
	return func(options *handlerOptions) { options.readiness = readiness }
}

// Server exposes process health over HTTP.
type Server struct {
	httpServer *http.Server
}

// NewHandler returns the process liveness handler.
func NewHandler(options ...HandlerOption) http.Handler {
	configuration := handlerOptions{}
	for _, option := range options {
		if option != nil {
			option(&configuration)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if configuration.readiness == nil || !configuration.readiness.Ready() {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	return mux
}

// NewServer creates a health server with explicit network timeouts.
func NewServer(address string, readTimeout, writeTimeout, idleTimeout time.Duration) *Server {
	return &Server{httpServer: &http.Server{
		Addr:         address,
		Handler:      NewHandler(),
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
		IdleTimeout:  idleTimeout,
	}}
}

// Run serves until ctx is cancelled or the server fails.
func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() { errCh <- s.httpServer.ListenAndServe() }()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), s.httpServer.WriteTimeout)
		defer cancel()
		if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
			return err
		}
		err := <-errCh
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
