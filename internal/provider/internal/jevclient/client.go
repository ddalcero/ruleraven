// Package jevclient implements the shared Jev Decisions wire protocol used by
// the direct TypeSafe and OpenRouter adapters.
package jevclient

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

type Mode string

const (
	ModeTypeSafe   Mode = "typesafe"
	ModeOpenRouter Mode = "openrouter"
)

type Config struct {
	Mode         Mode
	EndpointPath string
	DefaultURL   string
	DefaultModel string
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
	mode       Mode
}

func New(config Config) (*Client, error) {
	factory := config.Factory
	endpoint := strings.TrimSpace(factory.Endpoint)
	if endpoint == "" {
		endpoint = config.DefaultURL
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("provider endpoint must be an absolute HTTP URL")
	}
	model := strings.TrimSpace(factory.Model)
	if model == "" {
		model = config.DefaultModel
	}
	if model == "" {
		return nil, fmt.Errorf("provider model is required")
	}
	if strings.TrimSpace(factory.Credential) == "" {
		return nil, fmt.Errorf("provider credential is required")
	}
	if factory.HTTPClient == nil {
		return nil, fmt.Errorf("provider HTTP client is required")
	}
	if factory.MaxRequestBytes <= 0 || factory.MaxResponseBytes <= 0 {
		return nil, fmt.Errorf("provider HTTP limits must be positive")
	}
	if config.Mode != ModeTypeSafe && config.Mode != ModeOpenRouter {
		return nil, fmt.Errorf("unknown Jev provider mode")
	}
	if factory.Now == nil {
		factory.Now = time.Now
	}
	return &Client{
		name:       string(config.Mode),
		endpoint:   strings.TrimRight(endpoint, "/") + config.EndpointPath,
		model:      model,
		credential: factory.Credential,
		httpClient: factory.HTTPClient,
		retry:      factory.Retry,
		limits: provider.HTTPLimits{
			MaxRequestBytes: factory.MaxRequestBytes, MaxResponseBytes: factory.MaxResponseBytes,
			MaxRetryAfter: factory.Retry.MaxRetryAfter, Now: factory.Now,
		},
		now:  factory.Now,
		mode: config.Mode,
	}, nil
}

func (c *Client) Name() string { return c.name }

func (c *Client) Evaluate(ctx context.Context, request provider.EvaluationRequest) (provider.EvaluationResponse, error) {
	started := c.now()
	if err := provider.ValidateQuestions(request.Questions); err != nil {
		return provider.EvaluationResponse{}, fmt.Errorf("invalid provider questions: %w", err)
	}
	if strings.TrimSpace(request.RequestID) == "" {
		return provider.EvaluationResponse{}, fmt.Errorf("provider request ID is required")
	}
	state, err := decodeState(request.State)
	if err != nil {
		return provider.EvaluationResponse{}, err
	}
	questions := make(map[string]wireQuestion, len(request.Questions))
	for id, question := range request.Questions {
		translated := wireQuestion{Type: question.Type, Instructions: question.Instructions}
		switch question.Type {
		case provider.QuestionTypeNoul, provider.QuestionTypeChoice:
			if len(question.Criteria) != 0 {
				translated.Criteria = question.Criteria
			}
		case provider.QuestionTypeScore:
			translated.Criteria = question.Levels
		}
		questions[id] = translated
	}
	requestBody, err := json.Marshal(wireRequest{Model: c.model, State: state, Questions: questions})
	if err != nil {
		return provider.EvaluationResponse{}, errors.New("encode provider request")
	}

	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+c.credential)
	headers.Set("X-Request-ID", request.RequestID)
	var httpResponse provider.HTTPResponse
	attempts, err := provider.Retry(ctx, c.retry, func(ctx context.Context, _ int) error {
		httpResponse, err = provider.DoJSONResponse(ctx, c.httpClient, http.MethodPost, c.endpoint, headers, requestBody, c.limits)
		return err
	})
	if err != nil {
		return provider.EvaluationResponse{}, err
	}

	parsed, err := c.decodeResponse(httpResponse.Body)
	if err != nil {
		return provider.EvaluationResponse{}, err
	}
	if err := provider.ValidateAnswers(request.Questions, parsed.answers); err != nil {
		return provider.EvaluationResponse{}, fmt.Errorf("validate provider answers: %w", err)
	}
	if parsed.model == "" {
		return provider.EvaluationResponse{}, errors.New("decode provider response: model is required")
	}
	if parsed.usage.InputTokens < 0 || parsed.usage.OutputTokens < 0 {
		return provider.EvaluationResponse{}, errors.New("decode provider response: token usage must not be negative")
	}
	providerRequestID := parsed.id
	if providerRequestID == "" {
		providerRequestID = httpResponse.Header.Get("X-Request-ID")
	}
	hash := sha256.Sum256(httpResponse.Body)
	return provider.EvaluationResponse{
		Answers: parsed.answers, Provider: c.name, RequestedModel: c.model, ResolvedModel: parsed.model,
		RequestID: request.RequestID, ProviderRequestID: providerRequestID, Usage: parsed.usage,
		Latency: c.now().Sub(started), Attempts: attempts, RawResponseHash: hex.EncodeToString(hash[:]),
	}, nil
}

type wireRequest struct {
	Model     string                  `json:"model"`
	State     any                     `json:"state"`
	Questions map[string]wireQuestion `json:"questions"`
}

type wireQuestion struct {
	Type         provider.QuestionType `json:"type"`
	Instructions string                `json:"instructions"`
	Criteria     any                   `json:"criteria,omitempty"`
}

type wireUsage struct {
	InputTokens  *int     `json:"input_tokens"`
	OutputTokens *int     `json:"output_tokens"`
	Cost         *float64 `json:"cost,omitempty"`
}

type parsedResponse struct {
	id      string
	model   string
	answers map[string]provider.Answer
	usage   provider.Usage
}

func (c *Client) decodeResponse(body []byte) (parsedResponse, error) {
	switch c.mode {
	case ModeTypeSafe:
		var envelope struct {
			Model   string                     `json:"model"`
			Answers map[string]provider.Answer `json:"answers"`
			Usage   *wireUsage                 `json:"usage"`
		}
		if err := decodeStrict(body, &envelope); err != nil {
			return parsedResponse{}, errors.New("decode provider response")
		}
		usage, err := parseUsage(envelope.Usage)
		if err != nil {
			return parsedResponse{}, err
		}
		return parsedResponse{model: envelope.Model, answers: envelope.Answers, usage: usage}, nil
	case ModeOpenRouter:
		var envelope struct {
			ID       string                     `json:"id,omitempty"`
			Model    string                     `json:"model"`
			Provider string                     `json:"provider,omitempty"`
			Answers  map[string]provider.Answer `json:"answers"`
			Usage    *wireUsage                 `json:"usage"`
		}
		if err := decodeStrict(body, &envelope); err != nil {
			return parsedResponse{}, errors.New("decode provider response")
		}
		usage, err := parseUsage(envelope.Usage)
		if err != nil {
			return parsedResponse{}, err
		}
		return parsedResponse{id: envelope.ID, model: envelope.Model, answers: envelope.Answers, usage: usage}, nil
	default:
		panic("validated Jev provider mode")
	}
}

func parseUsage(usage *wireUsage) (provider.Usage, error) {
	if usage == nil || usage.InputTokens == nil || usage.OutputTokens == nil {
		return provider.Usage{}, errors.New("decode provider response: complete usage is required")
	}
	return provider.Usage{InputTokens: *usage.InputTokens, OutputTokens: *usage.OutputTokens}, nil
}

func decodeState(raw json.RawMessage) (any, error) {
	var state any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&state); err != nil {
		return nil, errors.New("provider state must be valid JSON")
	}
	if err := requireEOF(decoder); err != nil {
		return nil, errors.New("provider state must contain one JSON value")
	}
	switch state.(type) {
	case string, []any, map[string]any:
		return state, nil
	default:
		return nil, errors.New("provider state must be a JSON string, object, or array")
	}
}

func decodeStrict(body []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	return requireEOF(decoder)
}

func requireEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("unexpected trailing JSON value")
		}
		return err
	}
	return nil
}
