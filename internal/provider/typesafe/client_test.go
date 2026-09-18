package typesafe_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ddalcero/ruleraven/internal/provider"
	"github.com/ddalcero/ruleraven/internal/provider/typesafe"
)

func TestClientTranslatesRequestAndRecordsAuditMetadata(t *testing.T) {
	responseBody := []byte(`{"model":"jev-1.13.0","answers":{"urgent":{"type":"noul","noul":0.75},"cause":{"type":"choice","choice":"infra","probabilities":{"app":0.1,"infra":0.9},"confidence":0.8},"impact":{"type":"score","score":0.8,"legend":{"0":"low","1":"high"},"probabilities":{"0":0.2,"1":0.8},"confidence":0.7}},"usage":{"input_tokens":123,"output_tokens":7}}`)
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
		w.Header().Set("X-Request-ID", "typesafe-provider-request")
		_, _ = w.Write(responseBody)
	}))
	defer server.Close()

	clock := sequenceClock(
		time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC),
		time.Date(2026, 9, 18, 10, 0, 0, 25_000_000, time.UTC),
	)
	client, err := typesafe.New(testConfig(server, "jev-latest", clock))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	request := evaluationRequest()
	response, err := client.Evaluate(context.Background(), request)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}

	if gotPath != "/v1/systemone" {
		t.Errorf("path = %q, want /v1/systemone", gotPath)
	}
	if gotAuthorization != "Bearer credential-sentinel" {
		t.Errorf("Authorization = %q", gotAuthorization)
	}
	if gotRequestID != request.RequestID {
		t.Errorf("X-Request-ID = %q, want %q", gotRequestID, request.RequestID)
	}
	wantBody := map[string]any{
		"model": "jev-latest",
		"state": map[string]any{"event": "warning"},
		"questions": map[string]any{
			"urgent": map[string]any{"type": "noul", "instructions": "Is it urgent?", "criteria": map[string]any{"true": "urgent", "false": "not urgent"}},
			"cause":  map[string]any{"type": "choice", "instructions": "Choose cause.", "criteria": map[string]any{"app": "application", "infra": "infrastructure"}},
			"impact": map[string]any{"type": "score", "instructions": "Rate impact.", "criteria": []any{"low", "high"}},
		},
	}
	if !reflect.DeepEqual(gotBody, wantBody) {
		t.Errorf("request body = %#v, want %#v", gotBody, wantBody)
	}
	if response.Provider != "typesafe" || response.RequestedModel != "jev-latest" || response.ResolvedModel != "jev-1.13.0" {
		t.Errorf("model/provider metadata = %#v", response)
	}
	if response.RequestID != request.RequestID || response.ProviderRequestID != "typesafe-provider-request" {
		t.Errorf("request IDs = %q/%q", response.RequestID, response.ProviderRequestID)
	}
	if response.Usage != (provider.Usage{InputTokens: 123, OutputTokens: 7}) {
		t.Errorf("usage = %#v", response.Usage)
	}
	if response.Latency != 25*time.Millisecond || response.Attempts != 1 {
		t.Errorf("latency/attempts = %s/%d", response.Latency, response.Attempts)
	}
	hash := sha256.Sum256(responseBody)
	if response.RawResponseHash != hex.EncodeToString(hash[:]) {
		t.Errorf("raw hash = %q", response.RawResponseHash)
	}
	if err := provider.ValidateAnswers(request.Questions, response.Answers); err != nil {
		t.Errorf("answers invalid: %v", err)
	}
}

func TestClientRejectsMissingUsageFields(t *testing.T) {
	tests := map[string]string{
		"usage":         `{"model":"jev-1.13.0","answers":{"urgent":{"type":"noul","noul":0.75},"cause":{"type":"choice","choice":"infra","probabilities":{"app":0.1,"infra":0.9},"confidence":0.8},"impact":{"type":"score","score":0.8,"legend":{"0":"low","1":"high"},"probabilities":{"0":0.2,"1":0.8},"confidence":0.7}}}`,
		"input_tokens":  `{"model":"jev-1.13.0","answers":{"urgent":{"type":"noul","noul":0.75},"cause":{"type":"choice","choice":"infra","probabilities":{"app":0.1,"infra":0.9},"confidence":0.8},"impact":{"type":"score","score":0.8,"legend":{"0":"low","1":"high"},"probabilities":{"0":0.2,"1":0.8},"confidence":0.7}},"usage":{"output_tokens":7}}`,
		"output_tokens": `{"model":"jev-1.13.0","answers":{"urgent":{"type":"noul","noul":0.75},"cause":{"type":"choice","choice":"infra","probabilities":{"app":0.1,"infra":0.9},"confidence":0.8},"impact":{"type":"score","score":0.8,"legend":{"0":"low","1":"high"},"probabilities":{"0":0.2,"1":0.8},"confidence":0.7}},"usage":{"input_tokens":123}}`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }))
			defer server.Close()
			client, err := typesafe.New(testConfig(server, "jev-latest", time.Now))
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			if _, err := client.Evaluate(context.Background(), evaluationRequest()); err == nil {
				t.Fatal("Evaluate() error = nil, want missing usage rejection")
			}
		})
	}
}

func TestClientRejectsUnknownResponseFieldsWithoutLeakingRawBodyOrCredential(t *testing.T) {
	const credential = "credential-leak-sentinel"
	const raw = `{"model":"jev-1.13.0","answers":{},"usage":{"input_tokens":1,"output_tokens":0},"secret":"raw-body-leak-sentinel"}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, raw) }))
	defer server.Close()
	config := testConfig(server, "jev-latest", time.Now)
	config.Credential = credential
	client, err := typesafe.New(config)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = client.Evaluate(context.Background(), evaluationRequest())
	if err == nil {
		t.Fatal("Evaluate() error = nil")
	}
	for _, sentinel := range []string{credential, "raw-body-leak-sentinel"} {
		if strings.Contains(err.Error(), sentinel) {
			t.Fatalf("error leaks %q: %v", sentinel, err)
		}
	}
}

func TestRegisterAddsTypeSafeFactory(t *testing.T) {
	registry := provider.NewRegistry()
	if err := typesafe.Register(registry); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if got := registry.Types(); !reflect.DeepEqual(got, []string{"typesafe"}) {
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
		RubricVersion:  "triage-v1",
		RequestID:      "ruleraven-request",
		RequestedModel: "must-not-override-configured-model",
	}
}

func testConfig(server *httptest.Server, model string, now func() time.Time) provider.FactoryConfig {
	return provider.FactoryConfig{
		Endpoint: server.URL, Model: model, Credential: "credential-sentinel", HTTPClient: server.Client(),
		Retry:           provider.RetryPolicy{MaxAttempts: 3, InitialBackoff: time.Millisecond, MaxBackoff: time.Second, MaxRetryAfter: time.Second, Sleep: func(context.Context, time.Duration) error { return nil }},
		MaxRequestBytes: 1 << 20, MaxResponseBytes: 1 << 20, Now: now,
	}
}

func sequenceClock(times ...time.Time) func() time.Time {
	index := 0
	return func() time.Time {
		if index >= len(times) {
			return times[len(times)-1]
		}
		result := times[index]
		index++
		return result
	}
}
