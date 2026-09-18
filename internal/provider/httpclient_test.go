package provider

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDoJSONBoundsRequestBeforeSending(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls.Add(1)
	}))
	defer server.Close()

	_, err := DoJSON(context.Background(), server.Client(), http.MethodPost, server.URL, nil, []byte("12345"), HTTPLimits{
		MaxRequestBytes:  4,
		MaxResponseBytes: 16,
	})
	if err == nil || !errors.Is(err, ErrRequestTooLarge) {
		t.Fatalf("error = %v, want ErrRequestTooLarge", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("server calls = %d, want 0", calls.Load())
	}
}

func TestDoJSONBoundsSuccessfulResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "12345")
	}))
	defer server.Close()

	_, err := DoJSON(context.Background(), server.Client(), http.MethodPost, server.URL, nil, nil, HTTPLimits{
		MaxRequestBytes:  16,
		MaxResponseBytes: 4,
	})
	if err == nil || !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("error = %v, want ErrResponseTooLarge", err)
	}
}

func TestDoJSONReturnsBodyForSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer server.Close()

	body, err := DoJSON(context.Background(), server.Client(), http.MethodPost, server.URL, http.Header{
		"Authorization": []string{"Bearer secret"},
	}, []byte(`{"request":true}`), HTTPLimits{MaxRequestBytes: 64, MaxResponseBytes: 64})
	if err != nil {
		t.Fatalf("DoJSON() error = %v", err)
	}
	if got := string(body); got != `{"ok":true}` {
		t.Fatalf("body = %q", got)
	}
}

func TestDoJSONClassifiesStatusAndParsesBoundedRetryAfter(t *testing.T) {
	now := time.Date(2026, time.September, 18, 12, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", now.Add(30*time.Second).Format(http.TimeFormat))
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, "credential-and-raw-body-sentinel")
	}))
	defer server.Close()

	_, err := DoJSON(context.Background(), server.Client(), http.MethodPost, server.URL, http.Header{
		"Authorization": []string{"Bearer authorization-sentinel"},
	}, nil, HTTPLimits{
		MaxRequestBytes:  64,
		MaxResponseBytes: 64,
		MaxRetryAfter:    5 * time.Second,
		Now:              func() time.Time { return now },
	})
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("error = %T %v, want *HTTPError", err, err)
	}
	if httpErr.Kind != ErrorKindRateLimited || !httpErr.Retryable || httpErr.RetryAfter() != 5*time.Second {
		t.Fatalf("HTTPError = %#v", httpErr)
	}
	for _, sentinel := range []string{"credential-and-raw-body-sentinel", "authorization-sentinel"} {
		if strings.Contains(err.Error(), sentinel) {
			t.Fatalf("error leaks %q: %v", sentinel, err)
		}
	}
}

func TestDoJSONClassifiesTransportErrorWithoutLeakingIt(t *testing.T) {
	const sentinel = "transport-credential-sentinel"
	client := roundTripperClient{roundTrip: func(*http.Request) (*http.Response, error) {
		return nil, errors.New(sentinel)
	}}
	_, err := DoJSON(context.Background(), client, http.MethodPost, "https://provider.invalid", nil, nil, HTTPLimits{
		MaxRequestBytes:  64,
		MaxResponseBytes: 64,
	})
	if !IsRetryable(err) {
		t.Fatalf("error = %v, want retryable", err)
	}
	if strings.Contains(err.Error(), sentinel) {
		t.Fatalf("error leaks transport detail: %v", err)
	}
}

func TestDoJSONPreservesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := roundTripperClient{roundTrip: func(req *http.Request) (*http.Response, error) {
		return nil, req.Context().Err()
	}}
	_, err := DoJSON(ctx, client, http.MethodPost, "https://provider.invalid", nil, nil, HTTPLimits{
		MaxRequestBytes:  64,
		MaxResponseBytes: 64,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

type roundTripperClient struct {
	roundTrip func(*http.Request) (*http.Response, error)
}

func (c roundTripperClient) Do(request *http.Request) (*http.Response, error) {
	return c.roundTrip(request)
}
