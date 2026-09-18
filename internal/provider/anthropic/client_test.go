package anthropic_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/ddalcero/ruleraven/internal/provider"
	"github.com/ddalcero/ruleraven/internal/provider/anthropic"
)

func TestClientForcesExactlyOneSubmissionTool(t *testing.T) {
	var gotPath, gotAPIKey, gotVersion, gotRequestID string
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAPIKey, gotVersion = r.URL.Path, r.Header.Get("X-Api-Key"), r.Header.Get("Anthropic-Version")
		gotRequestID = r.Header.Get("X-Request-ID")
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode request: %v", err)
		}
		_, _ = io.WriteString(w, anthropicResponse([]any{toolUse("submit_answers", answersValue())}))
	}))
	defer server.Close()

	client, err := anthropic.New(testConfig(server))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	response, err := client.Evaluate(context.Background(), evaluationRequest())
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if gotPath != "/v1/messages" || gotAPIKey != "secret" || gotVersion == "" || gotRequestID != "request-123" {
		t.Fatalf("wire metadata = %q %q %q %q", gotPath, gotAPIKey, gotVersion, gotRequestID)
	}
	tools := got["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["name"] != "submit_answers" {
		t.Fatalf("tools = %#v", tools)
	}
	choice := got["tool_choice"].(map[string]any)
	if choice["type"] != "tool" || choice["name"] != "submit_answers" || choice["disable_parallel_tool_use"] != true {
		t.Fatalf("tool_choice = %#v", choice)
	}
	schema := tools[0].(map[string]any)["input_schema"].(map[string]any)
	if schema["additionalProperties"] != false {
		t.Fatalf("input schema is not strict: %#v", schema)
	}
	if response.Provider != "anthropic" || response.ProviderRequestID != "msg-123" || response.ResolvedModel != "resolved-claude" || response.Usage != (provider.Usage{InputTokens: 13, OutputTokens: 8}) || response.RawResponseHash == "" {
		t.Fatalf("response metadata = %#v", response)
	}
}

func TestClientRejectsMissingWrongAndMultipleToolUses(t *testing.T) {
	tests := []struct {
		name    string
		content []any
	}{
		{name: "missing", content: []any{map[string]any{"type": "text", "text": validAnswersJSON()}}},
		{name: "wrong", content: []any{toolUse("other_tool", answersValue())}},
		{name: "multiple", content: []any{toolUse("submit_answers", answersValue()), toolUse("submit_answers", answersValue())}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, anthropicResponse(test.content))
			}))
			defer server.Close()
			client, err := anthropic.New(testConfig(server))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.Evaluate(context.Background(), evaluationRequest()); err == nil {
				t.Fatal("Evaluate() error = nil")
			}
		})
	}
}

func TestRegisterAddsAnthropicFactory(t *testing.T) {
	registry := provider.NewRegistry()
	if err := anthropic.Register(registry); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if got := registry.Types(); !reflect.DeepEqual(got, []string{"anthropic"}) {
		t.Fatalf("types = %v", got)
	}
}

func toolUse(name string, input any) map[string]any {
	return map[string]any{"type": "tool_use", "id": "tool-1", "name": name, "input": input}
}

func answersValue() any {
	var value any
	_ = json.Unmarshal([]byte(validAnswersJSON()), &value)
	return value
}

func anthropicResponse(content []any) string {
	body, _ := json.Marshal(map[string]any{
		"id": "msg-123", "type": "message", "role": "assistant", "model": "resolved-claude", "stop_reason": "tool_use",
		"content": content, "usage": map[string]any{"input_tokens": 13, "output_tokens": 8},
	})
	return string(body)
}

func evaluationRequest() provider.EvaluationRequest {
	return provider.EvaluationRequest{
		State: json.RawMessage(`{"safe":"state"}`),
		Questions: map[string]provider.Question{
			"binary": {Type: provider.QuestionTypeNoul, Instructions: "Binary assessment?"},
			"choice": {Type: provider.QuestionTypeChoice, Instructions: "Choose.", Criteria: map[string]string{"application": "Application", "infrastructure": "Infrastructure"}},
			"score":  {Type: provider.QuestionTypeScore, Instructions: "Score.", Levels: []string{"low", "high"}},
		}, RubricVersion: "rubric-v1", RequestID: "request-123",
	}
}

func validAnswersJSON() string {
	return `{"answers":{"binary":{"type":"noul","noul":0.75},"choice":{"type":"choice","choice":"infrastructure","probabilities":{"application":0.1,"infrastructure":0.9},"confidence":0.9},"score":{"type":"score","score":0.8,"legend":{"0":"low","1":"high"},"probabilities":{"0":0.2,"1":0.8},"confidence":0.9}}}`
}

func testConfig(server *httptest.Server) provider.FactoryConfig {
	return provider.FactoryConfig{
		Endpoint: server.URL, Model: "configured-claude", Credential: "secret", HTTPClient: server.Client(),
		Retry:           provider.RetryPolicy{MaxAttempts: 3, InitialBackoff: time.Millisecond, MaxBackoff: time.Second, MaxRetryAfter: time.Second, Sleep: func(context.Context, time.Duration) error { return nil }},
		MaxRequestBytes: 1 << 20, MaxResponseBytes: 1 << 20, Now: time.Now,
	}
}
