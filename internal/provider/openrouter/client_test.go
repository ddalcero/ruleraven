package openrouter_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ddalcero/ruleraven/internal/provider"
	"github.com/ddalcero/ruleraven/internal/provider/openrouter"
)

func TestClientUsesDecisionsEndpointAndResolvedResponseMetadata(t *testing.T) {
	responseBody := `{"id":"gen-provider-123","model":"typesafe/jev-1.13.0","provider":"TypeSafe","answers":{"urgent":{"type":"noul","noul":0.75},"cause":{"type":"choice","choice":"infra","probabilities":{"app":0.1,"infra":0.9},"confidence":0.8},"impact":{"type":"score","score":0.8,"legend":{"0":"low","1":"high"},"probabilities":{"0":0.2,"1":0.8},"confidence":0.7}},"usage":{"input_tokens":50,"output_tokens":3,"cost":0.0001}}`
	var gotPath, gotAuthorization, gotRequestID string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		gotPath = request.URL.Path
		gotAuthorization = request.Header.Get("Authorization")
		gotRequestID = request.Header.Get("X-Request-ID")
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
			return
		}
		if err := json.Unmarshal(body, &gotBody); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		_, _ = io.WriteString(w, responseBody)
	}))
	defer server.Close()

	client, err := openrouter.New(testConfig(server))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	request := evaluationRequest()
	response, err := client.Evaluate(context.Background(), request)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if gotPath != "/api/alpha/decisions" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuthorization != "Bearer openrouter-credential" || gotRequestID != request.RequestID {
		t.Errorf("auth/request ID = %q/%q", gotAuthorization, gotRequestID)
	}
	if gotBody["model"] != "typesafe/jev-1.13" {
		t.Errorf("model = %#v", gotBody["model"])
	}
	if _, ok := gotBody["rubricVersion"]; ok {
		t.Error("request contains unsupported rubricVersion")
	}
	if response.Provider != "openrouter" || response.RequestedModel != "typesafe/jev-1.13" || response.ResolvedModel != "typesafe/jev-1.13.0" {
		t.Errorf("provider/models = %#v", response)
	}
	if response.ProviderRequestID != "gen-provider-123" || response.RequestID != request.RequestID {
		t.Errorf("request IDs = %q/%q", response.RequestID, response.ProviderRequestID)
	}
	if response.Usage != (provider.Usage{InputTokens: 50, OutputTokens: 3}) {
		t.Errorf("usage = %#v", response.Usage)
	}
}

func TestClientStrictlyRejectsMalformedOptionalEvidence(t *testing.T) {
	const rawSentinel = "raw-response-leak-sentinel"
	body := `{"id":"request-id","model":"typesafe/jev-1.13.0","answers":{"urgent":{"type":"noul","noul":0.75},"cause":{"type":"choice","choice":"infra","probabilities":"` + rawSentinel + `","confidence":0.8},"impact":{"type":"score","score":0.8,"legend":{"0":"low","1":"high"},"probabilities":{"0":0.2,"1":0.8},"confidence":0.7}},"usage":{"input_tokens":1,"output_tokens":1}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }))
	defer server.Close()
	client, err := openrouter.New(testConfig(server))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = client.Evaluate(context.Background(), evaluationRequest())
	if err == nil {
		t.Fatal("Evaluate() error = nil")
	}
	for _, sentinel := range []string{"openrouter-credential", rawSentinel} {
		if strings.Contains(err.Error(), sentinel) {
			t.Fatalf("error leaks %q: %v", sentinel, err)
		}
	}
}

func TestRegisterAddsOpenRouterFactory(t *testing.T) {
	registry := provider.NewRegistry()
	if err := openrouter.Register(registry); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if got := registry.Types(); !reflect.DeepEqual(got, []string{"openrouter"}) {
		t.Fatalf("types = %v", got)
	}
}

func evaluationRequest() provider.EvaluationRequest {
	return provider.EvaluationRequest{
		State: json.RawMessage(`{"event":"warning"}`),
		Questions: map[string]provider.Question{
			"urgent": {Type: provider.QuestionTypeNoul, Instructions: "Is it urgent?", Criteria: map[string]string{"true": "urgent", "false": "not urgent"}},
			"cause":  {Type: provider.QuestionTypeChoice, Instructions: "Choose cause.", Criteria: map[string]string{"app": "application", "infra": "infrastructure"}},
			"impact": {Type: provider.QuestionTypeScore, Instructions: "Rate impact.", Levels: []string{"low", "high"}},
		},
		RubricVersion: "triage-v1", RequestID: "ruleraven-request", RequestedModel: "must-not-override-configured-model",
	}
}

func testConfig(server *httptest.Server) provider.FactoryConfig {
	return provider.FactoryConfig{
		Endpoint: server.URL, Model: "typesafe/jev-1.13", Credential: "openrouter-credential", HTTPClient: server.Client(),
		Retry:           provider.RetryPolicy{MaxAttempts: 3, InitialBackoff: time.Millisecond, MaxBackoff: time.Second, MaxRetryAfter: time.Second, Sleep: func(context.Context, time.Duration) error { return nil }},
		MaxRequestBytes: 1 << 20, MaxResponseBytes: 1 << 20, Now: time.Now,
	}
}
