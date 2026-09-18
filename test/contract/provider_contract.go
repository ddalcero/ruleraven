// Package contract provides a reusable black-box provider adapter contract.
package contract

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ddalcero/ruleraven/internal/provider"
)

type AdapterConfig struct {
	Endpoint         string
	Credential       string
	Client           provider.HTTPClient
	Retry            provider.RetryPolicy
	MaxRequestBytes  int64
	MaxResponseBytes int64
}

type AdapterFactory func(AdapterConfig) (provider.Provider, error)

type Fixtures struct {
	Valid     []byte
	Missing   []byte
	Unknown   []byte
	BadNumber []byte
	Prose     []byte
	WrongTool []byte
}

type Suite struct {
	Name     string
	Factory  AdapterFactory
	Request  provider.EvaluationRequest
	Fixtures Fixtures
}

// RunProviderContract verifies behavior shared by every concrete provider
// adapter. Fixtures encode each adapter's wire protocol while assertions remain
// provider-neutral.
func RunProviderContract(t *testing.T, suite Suite) {
	t.Helper()
	validateSuite(t, suite)

	t.Run(suite.Name, func(t *testing.T) {
		t.Run("typed_answers", func(t *testing.T) {
			withServer(t, []serverReply{{body: suite.Fixtures.Valid}}, func(server *httptest.Server, _ *atomic.Int32) {
				adapter := newAdapter(t, suite, server, defaultConfig(server, noSleep))
				response, err := adapter.Evaluate(context.Background(), suite.Request)
				if err != nil {
					t.Fatalf("Evaluate() error = %v", err)
				}
				if err := provider.ValidateAnswers(suite.Request.Questions, response.Answers); err != nil {
					t.Fatalf("typed answers are invalid: %v", err)
				}
				for id, question := range suite.Request.Questions {
					if response.Answers[id].Type != question.Type {
						t.Errorf("answer %q type = %q, want %q", id, response.Answers[id].Type, question.Type)
					}
				}
				if response.Attempts != 1 {
					t.Errorf("attempts = %d, want 1", response.Attempts)
				}
				if response.RequestID != suite.Request.RequestID || response.RawResponseHash == "" {
					t.Errorf("response audit metadata is incomplete: %#v", response)
				}
			})
		})

		invalidFixtures := []struct {
			name string
			body []byte
		}{
			{name: "missing_answer", body: suite.Fixtures.Missing},
			{name: "unknown_answer", body: suite.Fixtures.Unknown},
			{name: "bad_number", body: suite.Fixtures.BadNumber},
			{name: "prose_around_JSON", body: suite.Fixtures.Prose},
			{name: "wrong_tool_name", body: suite.Fixtures.WrongTool},
		}
		for _, test := range invalidFixtures {
			t.Run(test.name, func(t *testing.T) {
				withServer(t, []serverReply{{body: test.body}}, func(server *httptest.Server, _ *atomic.Int32) {
					adapter := newAdapter(t, suite, server, defaultConfig(server, noSleep))
					if _, err := adapter.Evaluate(context.Background(), suite.Request); err == nil {
						t.Fatal("Evaluate() error = nil, want malformed response rejection")
					}
				})
			})
		}

		t.Run("retryable_status", func(t *testing.T) {
			withServer(t, []serverReply{{status: http.StatusServiceUnavailable}, {body: suite.Fixtures.Valid}}, func(server *httptest.Server, calls *atomic.Int32) {
				adapter := newAdapter(t, suite, server, defaultConfig(server, noSleep))
				response, err := adapter.Evaluate(context.Background(), suite.Request)
				if err != nil {
					t.Fatalf("Evaluate() error = %v", err)
				}
				if calls.Load() != 2 || response.Attempts != 2 {
					t.Fatalf("calls/attempts = %d/%d, want 2/2", calls.Load(), response.Attempts)
				}
			})
		})

		t.Run("permanent_status", func(t *testing.T) {
			withServer(t, []serverReply{{status: http.StatusUnauthorized}}, func(server *httptest.Server, calls *atomic.Int32) {
				adapter := newAdapter(t, suite, server, defaultConfig(server, noSleep))
				_, err := adapter.Evaluate(context.Background(), suite.Request)
				if err == nil || provider.IsRetryable(err) {
					t.Fatalf("error = %v, want permanent classified error", err)
				}
				if calls.Load() != 1 {
					t.Fatalf("calls = %d, want 1", calls.Load())
				}
			})
		})

		t.Run("bounded_Retry-After", func(t *testing.T) {
			var sleeps []time.Duration
			var mu sync.Mutex
			sleep := func(_ context.Context, delay time.Duration) error {
				mu.Lock()
				sleeps = append(sleeps, delay)
				mu.Unlock()
				return nil
			}
			withServer(t, []serverReply{{status: http.StatusTooManyRequests, retryAfter: "7"}, {body: suite.Fixtures.Valid}}, func(server *httptest.Server, _ *atomic.Int32) {
				adapter := newAdapter(t, suite, server, defaultConfig(server, sleep))
				if _, err := adapter.Evaluate(context.Background(), suite.Request); err != nil {
					t.Fatalf("Evaluate() error = %v", err)
				}
			})
			mu.Lock()
			defer mu.Unlock()
			if len(sleeps) != 1 || sleeps[0] != 2*time.Second {
				t.Fatalf("sleeps = %v, want [2s]", sleeps)
			}
		})

		t.Run("cancellation", func(t *testing.T) {
			withServer(t, []serverReply{{body: suite.Fixtures.Valid}}, func(server *httptest.Server, calls *atomic.Int32) {
				adapter := newAdapter(t, suite, server, defaultConfig(server, noSleep))
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				_, err := adapter.Evaluate(ctx, suite.Request)
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("error = %v, want context.Canceled", err)
				}
				if calls.Load() != 0 {
					t.Fatalf("calls = %d, want 0", calls.Load())
				}
			})
		})

		t.Run("response_size_limit", func(t *testing.T) {
			oversized := append(append([]byte(nil), suite.Fixtures.Valid...), []byte(strings.Repeat(" ", 128))...)
			withServer(t, []serverReply{{body: oversized}}, func(server *httptest.Server, _ *atomic.Int32) {
				config := defaultConfig(server, noSleep)
				config.MaxResponseBytes = int64(len(suite.Fixtures.Valid))
				adapter := newAdapter(t, suite, server, config)
				_, err := adapter.Evaluate(context.Background(), suite.Request)
				if !errors.Is(err, provider.ErrResponseTooLarge) {
					t.Fatalf("error = %v, want ErrResponseTooLarge", err)
				}
			})
		})

		t.Run("credential_leak_sentinels", func(t *testing.T) {
			const credential = "credential-leak-sentinel"
			const rawSentinel = "raw-response-leak-sentinel"
			const stateSentinel = "state-leak-sentinel"
			request := suite.Request
			request.State = []byte(`{"sensitive":"` + stateSentinel + `"}`)
			var authorization atomic.Value
			withServerObserved(t, []serverReply{{status: http.StatusBadRequest, body: []byte(rawSentinel + credential)}}, func(r *http.Request) {
				authorization.Store(r.Header.Get("Authorization"))
			}, func(server *httptest.Server, _ *atomic.Int32) {
				config := defaultConfig(server, noSleep)
				config.Credential = credential
				adapter := newAdapter(t, suite, server, config)
				_, err := adapter.Evaluate(context.Background(), request)
				if err == nil {
					t.Fatal("Evaluate() error = nil")
				}
				for _, sentinel := range []string{credential, rawSentinel, stateSentinel} {
					if strings.Contains(err.Error(), sentinel) {
						t.Fatalf("error leaks sentinel %q: %v", sentinel, err)
					}
				}
			})
			seenAuthorization, _ := authorization.Load().(string)
			if !strings.Contains(seenAuthorization, credential) {
				t.Fatal("credential sentinel was not exercised in request authorization")
			}
		})
	})
}

func validateSuite(t *testing.T, suite Suite) {
	t.Helper()
	if strings.TrimSpace(suite.Name) == "" || suite.Factory == nil {
		t.Fatal("contract suite requires a name and adapter factory")
	}
	if err := provider.ValidateQuestions(suite.Request.Questions); err != nil {
		t.Fatalf("contract request questions: %v", err)
	}
	fixtures := map[string][]byte{
		"valid": suite.Fixtures.Valid, "missing": suite.Fixtures.Missing,
		"unknown": suite.Fixtures.Unknown, "bad number": suite.Fixtures.BadNumber,
		"prose": suite.Fixtures.Prose, "wrong tool": suite.Fixtures.WrongTool,
	}
	for name, fixture := range fixtures {
		if len(fixture) == 0 {
			t.Fatalf("contract fixture %q is empty", name)
		}
	}
}

func newAdapter(t *testing.T, suite Suite, server *httptest.Server, config AdapterConfig) provider.Provider {
	t.Helper()
	adapter, err := suite.Factory(config)
	if err != nil {
		t.Fatalf("create adapter: %v", err)
	}
	if adapter == nil {
		t.Fatal("factory returned a nil adapter")
	}
	return adapter
}

func defaultConfig(server *httptest.Server, sleep provider.Sleeper) AdapterConfig {
	return AdapterConfig{
		Endpoint:         server.URL,
		Credential:       "contract-credential",
		Client:           server.Client(),
		MaxRequestBytes:  1 << 20,
		MaxResponseBytes: 1 << 20,
		Retry: provider.RetryPolicy{
			MaxAttempts:    3,
			InitialBackoff: 10 * time.Millisecond,
			MaxBackoff:     100 * time.Millisecond,
			MaxRetryAfter:  2 * time.Second,
			Sleep:          sleep,
		},
	}
}

func noSleep(context.Context, time.Duration) error { return nil }

type serverReply struct {
	status     int
	body       []byte
	retryAfter string
}

func withServer(t *testing.T, replies []serverReply, run func(*httptest.Server, *atomic.Int32)) {
	t.Helper()
	withServerObserved(t, replies, nil, run)
}

func withServerObserved(t *testing.T, replies []serverReply, observe func(*http.Request), run func(*httptest.Server, *atomic.Int32)) {
	t.Helper()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		call := int(calls.Add(1))
		if observe != nil {
			observe(request)
		}
		index := call - 1
		if index >= len(replies) {
			index = len(replies) - 1
		}
		reply := replies[index]
		if reply.retryAfter != "" {
			w.Header().Set("Retry-After", reply.retryAfter)
		}
		status := reply.status
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		_, _ = w.Write(reply.body)
	}))
	defer server.Close()
	run(server, &calls)
}
