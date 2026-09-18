package openaicompat_test

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
	"github.com/ddalcero/ruleraven/internal/provider/openaicompat"
)

func TestNewRequiresSupportedExplicitStrictMode(t *testing.T) {
	for _, mode := range []string{"", "json_object", "text", "JSON_SCHEMA"} {
		t.Run(mode, func(t *testing.T) {
			config := baseConfig(nil)
			config.StrictMode = mode
			_, err := openaicompat.New(config)
			if err == nil || !strings.Contains(err.Error(), "strict mode") {
				t.Fatalf("New() error = %v, want strict mode error", err)
			}
		})
	}
}

func TestJSONSchemaModeUsesStrictResponseFormat(t *testing.T) {
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = io.WriteString(w, responseWithContent(validAnswersJSON()))
	}))
	defer server.Close()
	config := baseConfig(server)
	config.StrictMode = openaicompat.ModeJSONSchema
	client, err := openaicompat.New(config)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Evaluate(context.Background(), evaluationRequest())
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	format := got["response_format"].(map[string]any)
	if format["type"] != "json_schema" || format["json_schema"].(map[string]any)["strict"] != true {
		t.Fatalf("response_format = %#v", format)
	}
	if _, exists := got["tools"]; exists {
		t.Fatalf("json_schema mode unexpectedly sent tools: %#v", got)
	}
	if response.Provider != "openai-compatible" || response.Answers["binary"].Noul == nil {
		t.Fatalf("response = %#v", response)
	}
}

func TestForcedToolModeForcesAndAcceptsOnlyOneNamedTool(t *testing.T) {
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = io.WriteString(w, responseWithTool("submit_answers", validAnswersJSON(), false))
	}))
	defer server.Close()
	config := baseConfig(server)
	config.StrictMode = openaicompat.ModeForcedTool
	client, err := openaicompat.New(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Evaluate(context.Background(), evaluationRequest()); err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	tools := got["tools"].([]any)
	choice := got["tool_choice"].(map[string]any)
	if len(tools) != 1 || choice["type"] != "function" || choice["function"].(map[string]any)["name"] != "submit_answers" || got["parallel_tool_calls"] != false {
		t.Fatalf("forced tool request = %#v", got)
	}
}

func TestForcedToolRejectsWrongMultipleAndPlainContent(t *testing.T) {
	tests := []struct{ name, response string }{
		{"wrong", responseWithTool("wrong", validAnswersJSON(), false)},
		{"multiple", responseWithTool("submit_answers", validAnswersJSON(), true)},
		{"plain_content", responseWithContent(validAnswersJSON())},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, test.response) }))
			defer server.Close()
			config := baseConfig(server)
			config.StrictMode = openaicompat.ModeForcedTool
			client, err := openaicompat.New(config)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.Evaluate(context.Background(), evaluationRequest()); err == nil {
				t.Fatal("Evaluate() error = nil")
			}
		})
	}
}

func TestRegisterAddsCompatibleFactory(t *testing.T) {
	registry := provider.NewRegistry()
	if err := openaicompat.Register(registry); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if got := registry.Types(); !reflect.DeepEqual(got, []string{"openai-compatible"}) {
		t.Fatalf("types = %v", got)
	}
}

func responseWithContent(content string) string {
	body, _ := json.Marshal(map[string]any{"id": "id-1", "model": "resolved-compatible", "choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": content}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 2}})
	return string(body)
}

func responseWithTool(name, arguments string, multiple bool) string {
	calls := []any{map[string]any{"id": "call-1", "type": "function", "function": map[string]any{"name": name, "arguments": arguments}}}
	if multiple {
		calls = append(calls, map[string]any{"id": "call-2", "type": "function", "function": map[string]any{"name": name, "arguments": arguments}})
	}
	body, _ := json.Marshal(map[string]any{"id": "id-1", "model": "resolved-compatible", "choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "tool_calls": calls}, "finish_reason": "tool_calls"}}, "usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 2}})
	return string(body)
}

func evaluationRequest() provider.EvaluationRequest {
	return provider.EvaluationRequest{State: json.RawMessage(`{"safe":"state"}`), Questions: map[string]provider.Question{
		"binary": {Type: provider.QuestionTypeNoul, Instructions: "Binary?"},
		"choice": {Type: provider.QuestionTypeChoice, Instructions: "Choose.", Criteria: map[string]string{"application": "Application", "infrastructure": "Infrastructure"}},
		"score":  {Type: provider.QuestionTypeScore, Instructions: "Score.", Levels: []string{"low", "high"}},
	}, RubricVersion: "v1", RequestID: "request-123"}
}

func validAnswersJSON() string {
	return `{"answers":{"binary":{"type":"noul","noul":0.75},"choice":{"type":"choice","choice":"infrastructure","probabilities":{"application":0.1,"infrastructure":0.9},"confidence":0.9},"score":{"type":"score","score":0.8,"legend":{"0":"low","1":"high"},"probabilities":{"0":0.2,"1":0.8},"confidence":0.9}}}`
}

func baseConfig(server *httptest.Server) provider.FactoryConfig {
	var endpoint string
	var client provider.HTTPClient = http.DefaultClient
	if server != nil {
		endpoint, client = server.URL, server.Client()
	} else {
		endpoint = "https://example.invalid"
	}
	return provider.FactoryConfig{Endpoint: endpoint, Model: "compatible-model", Credential: "secret", HTTPClient: client,
		Retry:           provider.RetryPolicy{MaxAttempts: 2, InitialBackoff: time.Millisecond, MaxBackoff: time.Second, MaxRetryAfter: time.Second, Sleep: func(context.Context, time.Duration) error { return nil }},
		MaxRequestBytes: 1 << 20, MaxResponseBytes: 1 << 20, Now: time.Now}
}
