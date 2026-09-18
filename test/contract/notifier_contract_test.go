package contract

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ddalcero/ruleraven/internal/notify"
	"github.com/ddalcero/ruleraven/internal/notify/webhook"
)

// RunNotifierContract verifies the security and delivery behavior required of
// every HTTP notifier factory.
func RunNotifierContract(t *testing.T, factory notify.Factory) {
	t.Helper()
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	publicLookup := func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("203.0.113.10")}}, nil
	}
	base := notify.FactoryConfig{
		DestinationID: "contract", Endpoint: "https://hooks.example.test/events",
		Secret: []byte("contract-secret"), Timeout: time.Second,
		MaxResponseBytes: 1024, MaxRetryAfter: time.Minute, LookupIP: publicLookup,
		Now: func() time.Time { return now },
	}

	t.Run("strict_config", func(t *testing.T) {
		mutations := []struct {
			name   string
			mutate func(*notify.FactoryConfig)
		}{
			{"destination", func(c *notify.FactoryConfig) { c.DestinationID = "" }},
			{"endpoint", func(c *notify.FactoryConfig) { c.Endpoint = "" }},
			{"secret", func(c *notify.FactoryConfig) { c.Secret = nil }},
			{"timeout", func(c *notify.FactoryConfig) { c.Timeout = 0 }},
			{"response_limit", func(c *notify.FactoryConfig) { c.MaxResponseBytes = 0 }},
			{"Retry-After_limit", func(c *notify.FactoryConfig) { c.MaxRetryAfter = 0 }},
			{"plain_HTTP", func(c *notify.FactoryConfig) { c.Endpoint = "http://hooks.example.test/events" }},
			{"userinfo", func(c *notify.FactoryConfig) { c.Endpoint = "https://user@hooks.example.test/events" }},
			{"fragment", func(c *notify.FactoryConfig) { c.Endpoint += "#fragment" }},
		}
		for _, test := range mutations {
			t.Run(test.name, func(t *testing.T) {
				config := base
				test.mutate(&config)
				if _, err := factory(config); err == nil {
					t.Fatal("factory accepted invalid configuration")
				}
			})
		}
	})

	t.Run("SSRF_policy", func(t *testing.T) {
		for _, rawIP := range []string{"127.0.0.1", "169.254.169.254", "10.0.0.1", "::1", "fe80::1"} {
			t.Run(rawIP, func(t *testing.T) {
				config := base
				config.LookupIP = func(context.Context, string) ([]net.IPAddr, error) {
					return []net.IPAddr{{IP: net.ParseIP(rawIP)}}, nil
				}
				if _, err := factory(config); err == nil {
					t.Fatalf("factory accepted SSRF destination %s", rawIP)
				}
			})
		}

		config := base
		config.HTTPClient = &http.Client{}
		if _, err := factory(config); err == nil {
			t.Fatal("factory accepted a client that bypasses its SSRF-safe dialer")
		}
	})

	t.Run("redirects_are_not_followed", func(t *testing.T) {
		var targetCalls atomic.Int32
		target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { targetCalls.Add(1) }))
		defer target.Close()
		redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target.URL, http.StatusFound)
		}))
		defer redirect.Close()
		n := newContractNotifier(t, factory, redirect, now)
		err := n.Deliver(context.Background(), contractEnvelope(t, now))
		var classified *notify.Error
		if !errors.As(err, &classified) || classified.Kind != "redirect" || classified.Retryable {
			t.Fatalf("Deliver() error = %#v, want permanent redirect", err)
		}
		if targetCalls.Load() != 0 {
			t.Fatal("redirect target was called")
		}
	})

	t.Run("retry_classification", func(t *testing.T) {
		cases := []struct {
			status    int
			retryable bool
		}{
			{http.StatusRequestTimeout, true}, {http.StatusConflict, true},
			{http.StatusTooEarly, true}, {http.StatusTooManyRequests, true},
			{http.StatusInternalServerError, true}, {http.StatusBadGateway, true},
			{600, false}, {http.StatusBadRequest, false}, {http.StatusUnauthorized, false},
			{http.StatusNotFound, false},
		}
		for _, test := range cases {
			t.Run(http.StatusText(test.status), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Retry-After", "3600")
					w.WriteHeader(test.status)
				}))
				defer server.Close()
				n := newContractNotifier(t, factory, server, now)
				err := n.Deliver(context.Background(), contractEnvelope(t, now))
				var classified *notify.Error
				if !errors.As(err, &classified) || classified.Retryable != test.retryable {
					t.Fatalf("Deliver() error = %#v, want retryable=%v", err, test.retryable)
				}
				if test.retryable && classified.RetryAfter() != time.Minute {
					t.Fatalf("RetryAfter() = %v, want 1m", classified.RetryAfter())
				}
			})
		}
	})

	t.Run("cancellation", func(t *testing.T) {
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
		defer server.Close()
		n := newContractNotifier(t, factory, server, now)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := n.Deliver(ctx, contractEnvelope(t, now)); !errors.Is(err, context.Canceled) {
			t.Fatalf("Deliver() error = %v, want context.Canceled", err)
		}
		if calls.Load() != 0 {
			t.Fatalf("server calls = %d, want 0", calls.Load())
		}
	})

	t.Run("errors_are_redacted", func(t *testing.T) {
		const secret = "secret-leak-sentinel"
		const response = "response-leak-sentinel"
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, response)
		}))
		defer server.Close()
		config := contractConfig(server, now)
		config.Secret = []byte(secret)
		n, err := factory(config)
		if err != nil {
			t.Fatalf("factory error = %v", err)
		}
		err = n.Deliver(context.Background(), contractEnvelope(t, now))
		if err == nil {
			t.Fatal("Deliver() error = nil")
		}
		for _, sentinel := range []string{secret, response, server.URL} {
			if strings.Contains(err.Error(), sentinel) {
				t.Fatalf("error leaks %q: %v", sentinel, err)
			}
		}
	})
}

func TestWebhookNotifierContract(t *testing.T) {
	registry := notify.NewRegistry()
	if err := webhook.Register(registry); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if got := registry.Types(); len(got) != 1 || got[0] != webhook.Type {
		t.Fatalf("registered types = %v", got)
	}
	RunNotifierContract(t, func(config notify.FactoryConfig) (notify.Notifier, error) {
		return registry.Create(webhook.Type, config)
	})
}

func newContractNotifier(t *testing.T, factory notify.Factory, server *httptest.Server, now time.Time) notify.Notifier {
	t.Helper()
	n, err := factory(contractConfig(server, now))
	if err != nil {
		t.Fatalf("factory error = %v", err)
	}
	return n
}

func contractConfig(server *httptest.Server, now time.Time) notify.FactoryConfig {
	client := *server.Client()
	return notify.FactoryConfig{
		DestinationID: "contract", Endpoint: server.URL, Secret: []byte("contract-secret"),
		Timeout: time.Second, MaxResponseBytes: 1024, MaxRetryAfter: time.Minute,
		AllowInsecureHTTP: true, AllowPrivateNetwork: true, HTTPClient: &client,
		LookupIP: net.DefaultResolver.LookupIPAddr, Now: func() time.Time { return now },
	}
}

func contractEnvelope(t *testing.T, now time.Time) notify.Envelope {
	t.Helper()
	event, err := notify.NewEnvelope("contract-event", "incident.opened", "contract-cluster", now, json.RawMessage(`{"evaluationId":"eval-1"}`))
	if err != nil {
		t.Fatal(err)
	}
	return event
}
