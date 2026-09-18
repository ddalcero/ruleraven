package openai_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ddalcero/ruleraven/internal/provider"
	"github.com/ddalcero/ruleraven/internal/provider/openai"
)

func TestClientSendsStrictJSONSchemaAndMapsAuditMetadata(t *testing.T) {
	var gotPath, gotAuthorization, gotRequestID string
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuthorization = r.Header.Get("Authorization")
		gotRequestID = r.Header.Get("X-Request-ID")
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode request: %v", err)
		}
		_, _ = io.WriteString(w, openAIResponse(validAnswersJSON()))
	}))
	defer server.Close()

	client, err := openai.New(testConfig(server, "configured-model"))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	response, err := client.Evaluate(context.Background(), evaluationRequest())
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if gotPath != "/v1/chat/completions" || gotAuthorization != "Bearer secret" || gotRequestID != "request-123" {
		t.Errorf("wire metadata = path %q auth %q request %q", gotPath, gotAuthorization, gotRequestID)
	}
	if got["model"] != "configured-model" {
		t.Fatalf("model = %#v", got["model"])
	}
	format := got["response_format"].(map[string]any)
	if format["type"] != "json_schema" {
		t.Fatalf("response_format type = %#v", format["type"])
	}
	definition := format["json_schema"].(map[string]any)
	if definition["strict"] != true || definition["name"] != "submit_answers" {
		t.Fatalf("json_schema = %#v", definition)
	}
	schema := definition["schema"].(map[string]any)
	if schema["additionalProperties"] != false {
		t.Fatalf("root schema is not closed: %#v", schema)
	}
	answers := schema["properties"].(map[string]any)["answers"].(map[string]any)
	if !reflect.DeepEqual(answers["required"], []any{"binary", "choice", "score"}) || answers["additionalProperties"] != false {
		t.Fatalf("answers schema = %#v", answers)
	}
	messages := got["messages"].([]any)
	user := messages[len(messages)-1].(map[string]any)["content"].(string)
	var universal provider.EvaluationRequest
	if err := json.Unmarshal([]byte(user), &universal); err != nil {
		t.Fatalf("user message is not universal request JSON: %v", err)
	}
	if string(universal.State) != `{"safe":"state"}` || len(universal.Questions) != 3 || universal.RubricVersion != "rubric-v1" {
		t.Fatalf("universal request was not preserved: %#v", universal)
	}
	if response.Provider != "openai" || response.RequestedModel != "configured-model" || response.ResolvedModel != "resolved-model" {
		t.Fatalf("provider/model metadata = %#v", response)
	}
	if response.ProviderRequestID != "chatcmpl-123" || response.RequestID != "request-123" || response.Usage.InputTokens != 11 || response.Usage.OutputTokens != 7 || response.RawResponseHash == "" || response.Attempts != 1 {
		t.Fatalf("audit metadata = %#v", response)
	}
}

func TestClientRejectsProseAndDoesNotRetryStructuredParsing(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = io.WriteString(w, openAIResponse(`Here is JSON: `+validAnswersJSON()))
	}))
	defer server.Close()
	client, err := openai.New(testConfig(server, "configured-model"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Evaluate(context.Background(), evaluationRequest()); err == nil {
		t.Fatal("Evaluate() error = nil")
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 (no plain JSON/text fallback)", calls)
	}
}

func TestClientBoundsStateBeforeNetwork(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer server.Close()
	config := testConfig(server, "configured-model")
	config.MaxRequestBytes = 512
	client, err := openai.New(config)
	if err != nil {
		t.Fatal(err)
	}
	request := evaluationRequest()
	request.State = json.RawMessage(`{"large":"` + strings.Repeat("x", 1024) + `"}`)
	_, err = client.Evaluate(context.Background(), request)
	if !errors.Is(err, provider.ErrRequestTooLarge) {
		t.Fatalf("Evaluate() error = %v, want ErrRequestTooLarge", err)
	}
	if calls != 0 {
		t.Fatalf("network calls = %d, want 0", calls)
	}
}

func TestClientDoesNotLeakMalformedSuccessfulResponse(t *testing.T) {
	const rawSentinel = "raw-response-leak-sentinel"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, openAIResponse(`{"answers":{"`+rawSentinel+`":{"type":"noul","noul":0.5}}}`))
	}))
	defer server.Close()
	client, err := openai.New(testConfig(server, "configured-model"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Evaluate(context.Background(), evaluationRequest())
	if err == nil {
		t.Fatal("Evaluate() error = nil")
	}
	for _, secret := range []string{rawSentinel, "secret"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error leaks %q: %v", secret, err)
		}
	}
}

func TestRegisterAddsOpenAIFactory(t *testing.T) {
	registry := provider.NewRegistry()
	if err := openai.Register(registry); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if got := registry.Types(); !reflect.DeepEqual(got, []string{"openai"}) {
		t.Fatalf("types = %v", got)
	}
}

func evaluationRequest() provider.EvaluationRequest {
	return provider.EvaluationRequest{
		State: json.RawMessage(`{"safe":"state"}`),
		Questions: map[string]provider.Question{
			"binary": {Type: provider.QuestionTypeNoul, Instructions: "Binary assessment?"},
			"choice": {Type: provider.QuestionTypeChoice, Instructions: "Choose.", Criteria: map[string]string{"application": "Application", "infrastructure": "Infrastructure"}},
			"score":  {Type: provider.QuestionTypeScore, Instructions: "Score.", Levels: []string{"low", "high"}},
		},
		RubricVersion: "rubric-v1", RequestID: "request-123", RequestedModel: "ignored-request-model",
	}
}

func validAnswersJSON() string {
	return `{"answers":{"binary":{"type":"noul","noul":0.75},"choice":{"type":"choice","choice":"infrastructure","probabilities":{"application":0.1,"infrastructure":0.9},"confidence":0.9},"score":{"type":"score","score":0.8,"legend":{"0":"low","1":"high"},"probabilities":{"0":0.2,"1":0.8},"confidence":0.9}}}`
}

func openAIResponse(content string) string {
	body, _ := json.Marshal(map[string]any{
		"id": "chatcmpl-123", "model": "resolved-model",
		"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": content}, "finish_reason": "stop"}},
		"usage":   map[string]any{"prompt_tokens": 11, "completion_tokens": 7},
	})
	return string(body)
}

func testConfig(server *httptest.Server, model string) provider.FactoryConfig {
	return provider.FactoryConfig{
		Endpoint: server.URL, Model: model, Credential: "secret", HTTPClient: server.Client(),
		Retry:           provider.RetryPolicy{MaxAttempts: 3, InitialBackoff: time.Millisecond, MaxBackoff: time.Second, MaxRetryAfter: time.Second, Sleep: func(context.Context, time.Duration) error { return nil }},
		MaxRequestBytes: 1 << 20, MaxResponseBytes: 1 << 20, Now: time.Now,
	}
}
