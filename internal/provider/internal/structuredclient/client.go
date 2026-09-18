// Package structuredclient implements the bounded structured-output HTTP flow
// shared by generative provider adapters.
package structuredclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ddalcero/ruleraven/internal/provider"
)

const toolName = "submit_answers"

type Protocol string

const (
	ProtocolOpenAIJSONSchema Protocol = "openai_json_schema"
	ProtocolOpenAIForcedTool Protocol = "openai_forced_tool"
	ProtocolAnthropicTool    Protocol = "anthropic_forced_tool"
)

type Config struct {
	Name         string
	DefaultURL   string
	EndpointPath string
	Protocol     Protocol
	Factory      provider.FactoryConfig
}

type Client struct {
	name       string
	endpoint   string
	model      string
	credential string
	httpClient provider.HTTPClient
	retry      provider.RetryPolicy
	limits     provider.HTTPLimits
	now        func() time.Time
	protocol   Protocol
}

func New(config Config) (*Client, error) {
	factory := config.Factory
	endpoint := strings.TrimSpace(factory.Endpoint)
	if endpoint == "" {
		endpoint = config.DefaultURL
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("provider endpoint must be an absolute HTTP URL")
	}
	model := strings.TrimSpace(factory.Model)
	if model == "" {
		return nil, errors.New("provider model is required")
	}
	if strings.TrimSpace(factory.Credential) == "" {
		return nil, errors.New("provider credential is required")
	}
	if factory.HTTPClient == nil {
		return nil, errors.New("provider HTTP client is required")
	}
	if factory.MaxRequestBytes <= 0 || factory.MaxResponseBytes <= 0 {
		return nil, errors.New("provider HTTP limits must be positive")
	}
	if config.Name == "" {
		return nil, errors.New("provider name is required")
	}
	switch config.Protocol {
	case ProtocolOpenAIJSONSchema, ProtocolOpenAIForcedTool, ProtocolAnthropicTool:
	default:
		return nil, errors.New("provider strict protocol is unsupported")
	}
	if factory.Now == nil {
		factory.Now = time.Now
	}
	return &Client{
		name: config.Name, endpoint: strings.TrimRight(endpoint, "/") + config.EndpointPath,
		model: model, credential: factory.Credential, httpClient: factory.HTTPClient,
		retry: factory.Retry, limits: provider.HTTPLimits{
			MaxRequestBytes: factory.MaxRequestBytes, MaxResponseBytes: factory.MaxResponseBytes,
			MaxRetryAfter: factory.Retry.MaxRetryAfter, Now: factory.Now,
		}, now: factory.Now, protocol: config.Protocol,
	}, nil
}

func (c *Client) Name() string { return c.name }

func (c *Client) Evaluate(ctx context.Context, request provider.EvaluationRequest) (provider.EvaluationResponse, error) {
	started := c.now()
	if err := provider.ValidateQuestions(request.Questions); err != nil {
		return provider.EvaluationResponse{}, fmt.Errorf("invalid provider questions: %w", err)
	}
	if strings.TrimSpace(request.RequestID) == "" {
		return provider.EvaluationResponse{}, errors.New("provider request ID is required")
	}
	if err := validateState(request.State); err != nil {
		return provider.EvaluationResponse{}, err
	}
	schemaBytes, err := provider.AnswerJSONSchema(request.Questions)
	if err != nil {
		return provider.EvaluationResponse{}, errors.New("build provider answer schema")
	}
	var schema any
	if err := json.Unmarshal(schemaBytes, &schema); err != nil {
		return provider.EvaluationResponse{}, errors.New("build provider answer schema")
	}
	prompt, err := json.Marshal(request)
	if err != nil {
		return provider.EvaluationResponse{}, errors.New("encode provider prompt")
	}
	body, err := c.requestBody(string(prompt), schema)
	if err != nil {
		return provider.EvaluationResponse{}, err
	}

	headers := make(http.Header)
	headers.Set("X-Request-ID", request.RequestID)
	if c.protocol == ProtocolAnthropicTool {
		headers.Set("X-Api-Key", c.credential)
		headers.Set("Anthropic-Version", "2023-06-01")
	} else {
		headers.Set("Authorization", "Bearer "+c.credential)
	}
	var response provider.HTTPResponse
	attempts, err := provider.Retry(ctx, c.retry, func(ctx context.Context, _ int) error {
		response, err = provider.DoJSONResponse(ctx, c.httpClient, http.MethodPost, c.endpoint, headers, body, c.limits)
		return err
	})
	if err != nil {
		return provider.EvaluationResponse{}, err
	}

	parsed, err := c.parseResponse(response.Body)
	if err != nil {
		return provider.EvaluationResponse{}, err
	}
	if err := provider.ValidateAnswers(request.Questions, parsed.answers); err != nil {
		// Answer values are provider-controlled, so never include validation detail.
		return provider.EvaluationResponse{}, errors.New("validate provider answers")
	}
	if parsed.model == "" || parsed.usage.InputTokens < 0 || parsed.usage.OutputTokens < 0 {
		return provider.EvaluationResponse{}, errors.New("decode provider response metadata")
	}
	providerRequestID := parsed.id
	if providerRequestID == "" {
		providerRequestID = response.Header.Get("X-Request-ID")
	}
	hash := sha256.Sum256(response.Body)
	return provider.EvaluationResponse{
		Answers: parsed.answers, Provider: c.name, RequestedModel: c.model, ResolvedModel: parsed.model,
		RequestID: request.RequestID, ProviderRequestID: providerRequestID, Usage: parsed.usage,
		Latency: c.now().Sub(started), Attempts: attempts, RawResponseHash: hex.EncodeToString(hash[:]),
	}, nil
}

func (c *Client) requestBody(prompt string, schema any) ([]byte, error) {
	var request any
	switch c.protocol {
	case ProtocolOpenAIJSONSchema:
		request = map[string]any{
			"model": c.model,
			"messages": []any{
				map[string]any{"role": "system", "content": "Return only the requested structured answers."},
				map[string]any{"role": "user", "content": prompt},
			},
			"response_format": map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": toolName, "strict": true, "schema": schema}},
		}
	case ProtocolOpenAIForcedTool:
		request = map[string]any{
			"model": c.model,
			"messages": []any{
				map[string]any{"role": "system", "content": "Submit exactly one structured answer tool call."},
				map[string]any{"role": "user", "content": prompt},
			},
			"tools":               []any{map[string]any{"type": "function", "function": map[string]any{"name": toolName, "description": "Submit all answers.", "strict": true, "parameters": schema}}},
			"tool_choice":         map[string]any{"type": "function", "function": map[string]any{"name": toolName}},
			"parallel_tool_calls": false,
		}
	case ProtocolAnthropicTool:
		request = map[string]any{
			"model": c.model, "max_tokens": 4096,
			"messages":    []any{map[string]any{"role": "user", "content": prompt}},
			"tools":       []any{map[string]any{"name": toolName, "description": "Submit all answers.", "input_schema": schema}},
			"tool_choice": map[string]any{"type": "tool", "name": toolName, "disable_parallel_tool_use": true},
		}
	default:
		panic("validated structured protocol")
	}
	body, err := json.Marshal(request)
	if err != nil {
		return nil, errors.New("encode provider request")
	}
	return body, nil
}

type parsedResponse struct {
	id      string
	model   string
	answers map[string]provider.Answer
	usage   provider.Usage
}

type openAIEnvelope struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Role      string `json:"role"`
			Content   string `json:"content"`
			ToolCalls []struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     *int `json:"prompt_tokens"`
		CompletionTokens *int `json:"completion_tokens"`
	} `json:"usage"`
}

type anthropicEnvelope struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Role       string `json:"role"`
	Model      string `json:"model"`
	StopReason string `json:"stop_reason"`
	Content    []struct {
		Type  string          `json:"type"`
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
		Text  string          `json:"text"`
	} `json:"content"`
	Usage *struct {
		InputTokens  *int `json:"input_tokens"`
		OutputTokens *int `json:"output_tokens"`
	} `json:"usage"`
}

func (c *Client) parseResponse(body []byte) (parsedResponse, error) {
	if c.protocol == ProtocolAnthropicTool {
		var envelope anthropicEnvelope
		if err := decodeOne(body, &envelope, false); err != nil || len(envelope.Content) != 1 || envelope.Type != "message" || envelope.Role != "assistant" || envelope.StopReason != "tool_use" {
			return parsedResponse{}, errors.New("decode provider response")
		}
		block := envelope.Content[0]
		if block.Type != "tool_use" || block.Name != toolName || len(block.Input) == 0 {
			return parsedResponse{}, errors.New("provider response did not use required answer tool")
		}
		answers, err := decodeAnswers(block.Input)
		if err != nil {
			return parsedResponse{}, err
		}
		usage, err := anthropicUsage(envelope.Usage)
		if err != nil {
			return parsedResponse{}, err
		}
		return parsedResponse{id: envelope.ID, model: envelope.Model, answers: answers, usage: usage}, nil
	}

	var envelope openAIEnvelope
	if err := decodeOne(body, &envelope, false); err != nil || len(envelope.Choices) != 1 {
		return parsedResponse{}, errors.New("decode provider response")
	}
	message := envelope.Choices[0].Message
	if message.Role != "assistant" {
		return parsedResponse{}, errors.New("decode provider response role")
	}
	var raw []byte
	if c.protocol == ProtocolOpenAIJSONSchema {
		if len(message.ToolCalls) != 0 || strings.TrimSpace(message.Content) == "" || envelope.Choices[0].FinishReason != "stop" {
			return parsedResponse{}, errors.New("decode provider structured response")
		}
		raw = []byte(message.Content)
	} else {
		if message.Content != "" || len(message.ToolCalls) != 1 || envelope.Choices[0].FinishReason != "tool_calls" {
			return parsedResponse{}, errors.New("provider response did not use exactly one required answer tool")
		}
		call := message.ToolCalls[0]
		if call.Type != "function" || call.Function.Name != toolName || strings.TrimSpace(call.Function.Arguments) == "" {
			return parsedResponse{}, errors.New("provider response used wrong answer tool")
		}
		raw = []byte(call.Function.Arguments)
	}
	answers, err := decodeAnswers(raw)
	if err != nil {
		return parsedResponse{}, err
	}
	usage, err := openAIUsage(envelope.Usage)
	if err != nil {
		return parsedResponse{}, err
	}
	return parsedResponse{id: envelope.ID, model: envelope.Model, answers: answers, usage: usage}, nil
}

func decodeAnswers(raw []byte) (map[string]provider.Answer, error) {
	var payload struct {
		Answers map[string]provider.Answer `json:"answers"`
	}
	if err := decodeOne(raw, &payload, true); err != nil || payload.Answers == nil {
		return nil, errors.New("decode provider structured answers")
	}
	return payload.Answers, nil
}

func openAIUsage(usage *struct {
	PromptTokens     *int `json:"prompt_tokens"`
	CompletionTokens *int `json:"completion_tokens"`
}) (provider.Usage, error) {
	if usage == nil || usage.PromptTokens == nil || usage.CompletionTokens == nil {
		return provider.Usage{}, errors.New("decode provider response usage")
	}
	return provider.Usage{InputTokens: *usage.PromptTokens, OutputTokens: *usage.CompletionTokens}, nil
}

func anthropicUsage(usage *struct {
	InputTokens  *int `json:"input_tokens"`
	OutputTokens *int `json:"output_tokens"`
}) (provider.Usage, error) {
	if usage == nil || usage.InputTokens == nil || usage.OutputTokens == nil {
		return provider.Usage{}, errors.New("decode provider response usage")
	}
	return provider.Usage{InputTokens: *usage.InputTokens, OutputTokens: *usage.OutputTokens}, nil
}

func validateState(raw json.RawMessage) error {
	var value any
	if err := decodeOne(raw, &value, false); err != nil {
		return errors.New("provider state must contain one valid JSON value")
	}
	switch value.(type) {
	case string, []any, map[string]any:
		return nil
	default:
		return errors.New("provider state must be a JSON string, object, or array")
	}
}

func decodeOne(raw []byte, destination any, strict bool) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if strict {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("unexpected trailing JSON")
	}
	return nil
}
