package contract

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/ddalcero/ruleraven/internal/provider"
)

func TestProviderContract(t *testing.T) {
	RunProviderContract(t, Suite{
		Name:    "reference",
		Factory: newReferenceAdapter,
		Request: contractRequest(),
		Fixtures: Fixtures{
			Valid:     responseJSON(validAnswers()),
			Missing:   responseJSON(map[string]provider.Answer{"binary": validAnswers()["binary"]}),
			Unknown:   responseJSON(withAnswer(validAnswers(), "unknown", provider.Answer{Type: provider.QuestionTypeNoul, Noul: float64Pointer(0.5)})),
			BadNumber: responseJSON(withAnswer(validAnswers(), "binary", provider.Answer{Type: provider.QuestionTypeNoul, Noul: float64Pointer(2)})),
			Prose:     append([]byte("Here is the result: "), responseJSON(validAnswers())...),
			WrongTool: toolResponseJSON("not_submit_answers", validAnswers()),
		},
	})
}

func newReferenceAdapter(config AdapterConfig) (provider.Provider, error) {
	return &referenceAdapter{config: config}, nil
}

type referenceAdapter struct{ config AdapterConfig }

func (*referenceAdapter) Name() string { return "reference" }

func (a *referenceAdapter) Evaluate(ctx context.Context, request provider.EvaluationRequest) (provider.EvaluationResponse, error) {
	requestBody, err := json.Marshal(request)
	if err != nil {
		return provider.EvaluationResponse{}, fmt.Errorf("encode provider request")
	}
	var body []byte
	attempts, err := provider.Retry(ctx, a.config.Retry, func(ctx context.Context, _ int) error {
		body, err = provider.DoJSON(ctx, a.config.Client, http.MethodPost, a.config.Endpoint, http.Header{
			"Authorization": []string{"Bearer " + a.config.Credential},
		}, requestBody, provider.HTTPLimits{
			MaxRequestBytes:  a.config.MaxRequestBytes,
			MaxResponseBytes: a.config.MaxResponseBytes,
			MaxRetryAfter:    a.config.Retry.MaxRetryAfter,
		})
		return err
	})
	if err != nil {
		return provider.EvaluationResponse{}, err
	}
	var envelope struct {
		Tool          string                     `json:"tool"`
		Answers       map[string]provider.Answer `json:"answers"`
		ResolvedModel string                     `json:"resolvedModel"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return provider.EvaluationResponse{}, errors.New("decode provider response")
	}
	if envelope.Tool != "submit_answers" {
		return provider.EvaluationResponse{}, errors.New("provider response used wrong tool")
	}
	if err := provider.ValidateAnswers(request.Questions, envelope.Answers); err != nil {
		return provider.EvaluationResponse{}, fmt.Errorf("validate provider answers: %w", err)
	}
	hash := sha256.Sum256(body)
	return provider.EvaluationResponse{
		Answers:         envelope.Answers,
		Provider:        a.Name(),
		RequestedModel:  request.RequestedModel,
		ResolvedModel:   envelope.ResolvedModel,
		RequestID:       request.RequestID,
		Attempts:        attempts,
		RawResponseHash: hex.EncodeToString(hash[:]),
	}, nil
}

func contractRequest() provider.EvaluationRequest {
	return provider.EvaluationRequest{
		State: json.RawMessage(`{"safe":"state"}`),
		Questions: map[string]provider.Question{
			"binary": {
				Type: provider.QuestionTypeNoul, Instructions: "Binary assessment?",
			},
			"choice": {
				Type: provider.QuestionTypeChoice, Instructions: "Choose a cause.",
				Criteria: map[string]string{"application": "Application", "infrastructure": "Infrastructure"},
			},
			"score": {
				Type: provider.QuestionTypeScore, Instructions: "Rate impact.",
				Levels: []string{"low", "high"},
			},
		},
		RubricVersion:  "contract-v1",
		RequestID:      "contract-request",
		RequestedModel: "contract-model",
	}
}

func validAnswers() map[string]provider.Answer {
	confidence := 0.9
	return map[string]provider.Answer{
		"binary": {Type: provider.QuestionTypeNoul, Noul: float64Pointer(0.75)},
		"choice": {
			Type: provider.QuestionTypeChoice, Choice: "infrastructure",
			Probabilities: map[string]float64{"application": 0.1, "infrastructure": 0.9}, Confidence: &confidence,
		},
		"score": {
			Type: provider.QuestionTypeScore, Score: float64Pointer(0.8),
			Legend:        map[string]string{"0": "low", "1": "high"},
			Probabilities: map[string]float64{"0": 0.2, "1": 0.8}, Confidence: &confidence,
		},
	}
}

func responseJSON(answers map[string]provider.Answer) []byte {
	return toolResponseJSON("submit_answers", answers)
}

func toolResponseJSON(tool string, answers map[string]provider.Answer) []byte {
	body, err := json.Marshal(map[string]any{
		"tool":          tool,
		"answers":       answers,
		"resolvedModel": "resolved-contract-model",
	})
	if err != nil {
		panic(err)
	}
	return body
}

func withAnswer(source map[string]provider.Answer, id string, answer provider.Answer) map[string]provider.Answer {
	result := make(map[string]provider.Answer, len(source)+1)
	for key, value := range source {
		result[key] = value
	}
	result[id] = answer
	return result
}

func float64Pointer(value float64) *float64 { return &value }
