package webhook_test

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

func TestDeliverSendsExactSignedCloudEvent(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	secret := []byte("webhook-secret")
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if got := r.Header.Get("Content-Type"); got != "application/cloudevents+json" {
			t.Errorf("Content-Type = %q", got)
		}
		if got := r.Header.Get("X-RuleRaven-Event-ID"); got != "delivery-1" {
			t.Errorf("event ID = %q", got)
		}
		if got := r.Header.Get("X-RuleRaven-Timestamp"); got != "1789732800" {
			t.Errorf("timestamp = %q", got)
		}
		signature := r.Header.Get("X-RuleRaven-Signature")
		if !strings.HasPrefix(signature, "v1=") || !notify.Verify(secret, now, body, strings.TrimPrefix(signature, "v1=")) {
			t.Errorf("invalid signature %q for body %s", signature, body)
		}
		var envelope notify.Envelope
		if err := json.Unmarshal(body, &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.ID != "delivery-1" || envelope.SchemaVersion != "v1" {
			t.Errorf("envelope = %#v", envelope)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	n := newTestNotifier(t, server, secret, now)
	event, _ := notify.NewEnvelope("delivery-1", "incident.opened", "cluster-a", now, json.RawMessage(`{"evaluationId":"eval-1"}`))
	if err := n.Deliver(context.Background(), event); err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d", calls.Load())
	}
}

func TestNewRejectsUnsafeOrIncompleteConfig(t *testing.T) {
	publicLookup := func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("203.0.113.10")}}, nil
	}
	base := notify.FactoryConfig{DestinationID: "ops", Endpoint: "https://hooks.example.test/events", Secret: []byte("secret"), Timeout: time.Second, MaxResponseBytes: 1024, MaxRetryAfter: time.Minute, LookupIP: publicLookup}
	tests := []struct {
		name   string
		mutate func(*notify.FactoryConfig)
	}{
		{"missing destination", func(c *notify.FactoryConfig) { c.DestinationID = "" }},
		{"missing secret", func(c *notify.FactoryConfig) { c.Secret = nil }},
		{"plain HTTP", func(c *notify.FactoryConfig) { c.Endpoint = "http://hooks.example.test/events" }},
		{"userinfo", func(c *notify.FactoryConfig) { c.Endpoint = "https://user@hooks.example.test/events" }},
		{"fragment", func(c *notify.FactoryConfig) { c.Endpoint = "https://hooks.example.test/events#frag" }},
		{"zero timeout", func(c *notify.FactoryConfig) { c.Timeout = 0 }},
		{"zero response bound", func(c *notify.FactoryConfig) { c.MaxResponseBytes = 0 }},
		{"zero retry after", func(c *notify.FactoryConfig) { c.MaxRetryAfter = 0 }},
		{"negative retry after", func(c *notify.FactoryConfig) { c.MaxRetryAfter = -1 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := base
			tt.mutate(&config)
			if _, err := webhook.New(config); err == nil {
				t.Fatal("New() accepted invalid config")
			}
		})
	}
}

func TestNewRejectsInjectedClientWhenPrivateNetworksAreBlocked(t *testing.T) {
	config := validConfig()
	config.HTTPClient = &http.Client{}
	if _, err := webhook.New(config); err == nil {
		t.Fatal("New() accepted an injected client that bypasses the SSRF-safe dialer")
	}
}

func TestNewBlocksSSRFAddressesByDefault(t *testing.T) {
	blocked := []string{"127.0.0.1", "169.254.169.254", "10.0.0.1", "0.0.0.0", "::1", "fc00::1"}
	for _, rawIP := range blocked {
		t.Run(rawIP, func(t *testing.T) {
			config := validConfig()
			config.LookupIP = func(context.Context, string) ([]net.IPAddr, error) {
				return []net.IPAddr{{IP: net.ParseIP(rawIP)}}, nil
			}
			if _, err := webhook.New(config); err == nil {
				t.Fatalf("New() accepted %s", rawIP)
			}
		})
	}
}

func TestDeliverRejectsRedirectWithoutFollowing(t *testing.T) {
	var targetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { targetCalls.Add(1) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer redirect.Close()
	n := newTestNotifier(t, redirect, []byte("secret"), time.Now())
	event, _ := notify.NewEnvelope("id", "type", "cluster", time.Now(), json.RawMessage(`{}`))
	err := n.Deliver(context.Background(), event)
	var classified *notify.Error
	if !errors.As(err, &classified) || classified.Retryable || classified.Kind != "redirect" {
		t.Fatalf("Deliver() error = %#v", err)
	}
	if targetCalls.Load() != 0 {
		t.Fatal("redirect target was called")
	}
}

func TestDeliverClassifiesStatusesAndBoundsRetryAfter(t *testing.T) {
	tests := []struct {
		status    int
		retryable bool
	}{{200, false}, {299, false}, {408, true}, {409, true}, {425, true}, {429, true}, {500, true}, {599, true}, {600, false}, {400, false}, {401, false}, {404, false}}
	for _, tt := range tests {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Retry-After", "3600")
				w.WriteHeader(tt.status)
			}))
			defer server.Close()
			n := newTestNotifier(t, server, []byte("secret"), time.Now())
			event, _ := notify.NewEnvelope("id", "type", "cluster", time.Now(), json.RawMessage(`{}`))
			err := n.Deliver(context.Background(), event)
			if tt.status >= 200 && tt.status <= 299 {
				if err != nil {
					t.Fatalf("error = %v", err)
				}
				return
			}
			var classified *notify.Error
			if !errors.As(err, &classified) || classified.Retryable != tt.retryable {
				t.Fatalf("error = %#v, retryable want %v", err, tt.retryable)
			}
			if tt.retryable && classified.RetryAfter() != time.Minute {
				t.Fatalf("RetryAfter = %v", classified.RetryAfter())
			}
		})
	}
}

func TestDeliverBoundsHugeRetryAfterWithoutOverflow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "9223372036854775807")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	n := newTestNotifier(t, server, []byte("secret"), time.Now())
	event, _ := notify.NewEnvelope("id", "type", "cluster", time.Now(), json.RawMessage(`{}`))
	var classified *notify.Error
	if err := n.Deliver(context.Background(), event); !errors.As(err, &classified) {
		t.Fatalf("Deliver() error = %#v", err)
	}
	if got := classified.RetryAfter(); got != time.Minute {
		t.Fatalf("RetryAfter() = %v, want 1m", got)
	}
}

func TestDeliverEnforcesResponseLimitTimeoutAndCancellation(t *testing.T) {
	t.Run("response limit", func(t *testing.T) {
		for _, test := range []struct {
			name          string
			status        int
			wantError     bool
			wantRetryable bool
		}{
			{name: "successful status remains successful", status: http.StatusOK},
			{name: "permanent status remains permanent", status: http.StatusBadRequest, wantError: true},
			{name: "retryable status remains retryable", status: http.StatusServiceUnavailable, wantError: true, wantRetryable: true},
		} {
			t.Run(test.name, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(test.status)
					_, _ = io.WriteString(w, strings.Repeat("x", 128))
				}))
				defer server.Close()
				config := testConfig(server, []byte("secret"), time.Now())
				config.MaxResponseBytes = 16
				n, err := webhook.New(config)
				if err != nil {
					t.Fatal(err)
				}
				event, _ := notify.NewEnvelope("id", "type", "cluster", time.Now(), json.RawMessage(`{}`))
				err = n.Deliver(context.Background(), event)
				if !test.wantError {
					if err != nil {
						t.Fatalf("Deliver() error = %v", err)
					}
					return
				}
				var classified *notify.Error
				if !errors.As(err, &classified) || classified.Kind != "response_too_large" || classified.Retryable != test.wantRetryable {
					t.Fatalf("error = %#v, want response_too_large retryable=%v", err, test.wantRetryable)
				}
			})
		}
	})
	t.Run("timeout", func(t *testing.T) {
		release := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-release }))
		defer server.Close()
		defer close(release)
		config := testConfig(server, []byte("secret"), time.Now())
		config.Timeout = 10 * time.Millisecond
		n, err := webhook.New(config)
		if err != nil {
			t.Fatal(err)
		}
		event, _ := notify.NewEnvelope("id", "type", "cluster", time.Now(), json.RawMessage(`{}`))
		if err := n.Deliver(context.Background(), event); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		defer server.Close()
		n := newTestNotifier(t, server, []byte("secret"), time.Now())
		event, _ := notify.NewEnvelope("id", "type", "cluster", time.Now(), json.RawMessage(`{}`))
		if err := n.Deliver(ctx, event); !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestRegisterAddsWebhookFactory(t *testing.T) {
	registry := notify.NewRegistry()
	if err := webhook.Register(registry); err != nil {
		t.Fatal(err)
	}
	if got := registry.Types(); len(got) != 1 || got[0] != webhook.Type {
		t.Fatalf("types = %v", got)
	}
}

func newTestNotifier(t *testing.T, server *httptest.Server, secret []byte, now time.Time) notify.Notifier {
	t.Helper()
	config := testConfig(server, secret, now)
	n, err := webhook.New(config)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return n
}

func testConfig(server *httptest.Server, secret []byte, now time.Time) notify.FactoryConfig {
	client := *server.Client()
	return notify.FactoryConfig{DestinationID: "ops", Endpoint: server.URL, Secret: secret, Timeout: time.Second, MaxResponseBytes: 1024, MaxRetryAfter: time.Minute, AllowInsecureHTTP: true, AllowPrivateNetwork: true, HTTPClient: &client, LookupIP: net.DefaultResolver.LookupIPAddr, Now: func() time.Time { return now }}
}

func validConfig() notify.FactoryConfig {
	return notify.FactoryConfig{DestinationID: "ops", Endpoint: "https://hooks.example.test/events", Secret: []byte("secret"), Timeout: time.Second, MaxResponseBytes: 1024, MaxRetryAfter: time.Minute, LookupIP: func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("203.0.113.10")}}, nil
	}}
}
