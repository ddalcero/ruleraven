package health_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ddalcero/ruleraven/internal/health"
)

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
