package health_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ddalcero/ruleraven/internal/health"
)

func TestReadinessRequiresStartupDependenciesAndIgnoresRuntimeOutages(t *testing.T) {
	readiness := health.NewReadiness()
	handler := health.NewHandler(health.WithReadiness(readiness))

	assertProbe := func(path string, want int) {
		t.Helper()
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != want {
			t.Fatalf("%s status = %d, want %d (body %q)", path, rec.Code, want, rec.Body.String())
		}
	}

	assertProbe("/healthz", http.StatusOK)
	assertProbe("/readyz", http.StatusServiceUnavailable)
	readiness.MarkConfigValidated()
	assertProbe("/readyz", http.StatusServiceUnavailable)
	readiness.MarkMongoReady()
	assertProbe("/readyz", http.StatusServiceUnavailable)
	readiness.MarkInformersSynced()
	assertProbe("/readyz", http.StatusOK)

	// Provider and webhook availability are deliberately absent from readiness:
	// runtime dependency outages must not restart a healthy worker.
	assertProbe("/readyz", http.StatusOK)
	assertProbe("/healthz", http.StatusOK)
}

func TestReadinessRejectsWrongMethodWithoutAffectingLiveness(t *testing.T) {
	readiness := health.NewReadiness()
	handler := health.NewHandler(health.WithReadiness(readiness))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/readyz", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("liveness status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestHandlerHealthz(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		status int
		body   string
	}{
		{name: "healthy", method: http.MethodGet, path: "/healthz", status: http.StatusOK, body: "ok\n"},
		{name: "unknown path", method: http.MethodGet, path: "/missing", status: http.StatusNotFound, body: "404 page not found\n"},
		{name: "wrong method", method: http.MethodPost, path: "/healthz", status: http.StatusMethodNotAllowed, body: "Method Not Allowed\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			rec := httptest.NewRecorder()
			health.NewHandler().ServeHTTP(rec, req)
			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d", rec.Code, tt.status)
			}
			if rec.Body.String() != tt.body {
				t.Fatalf("body = %q, want %q", rec.Body.String(), tt.body)
			}
		})
	}
}
