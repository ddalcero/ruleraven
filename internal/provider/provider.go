package provider

import (
	"context"
	"encoding/json"
	"time"
)

type QuestionType string

const (
	QuestionTypeNoul   QuestionType = "noul"
	QuestionTypeChoice QuestionType = "choice"
	QuestionTypeScore  QuestionType = "score"
)

// Question is a provider-neutral semantic question. Criteria is used by Noul
// and Choice questions; Levels is the ordered rubric for Score questions.
type Question struct {
	Type         QuestionType      `json:"type"`
	Instructions string            `json:"instructions"`
	Criteria     map[string]string `json:"criteria,omitempty"`
	Levels       []string          `json:"levels,omitempty"`
}

// Answer is the tagged union returned by a Provider. Validation rejects every
// field that is not part of the answer's declared type.
type Answer struct {
	Type          QuestionType       `json:"type"`
	Noul          *float64           `json:"noul,omitempty"`
	Choice        string             `json:"choice,omitempty"`
	Score         *float64           `json:"score,omitempty"`
	Legend        map[string]string  `json:"legend,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Confidence    *float64           `json:"confidence,omitempty"`
}

type EvaluationRequest struct {
	State          json.RawMessage     `json:"state"`
	Questions      map[string]Question `json:"questions"`
	RubricVersion  string              `json:"rubricVersion"`
	RequestID      string              `json:"requestId"`
	RequestedModel string              `json:"requestedModel,omitempty"`
}

type Usage struct {
	InputTokens  int `json:"inputTokens,omitempty"`
	OutputTokens int `json:"outputTokens,omitempty"`
}

type EvaluationResponse struct {
	Answers           map[string]Answer `json:"answers"`
	Provider          string            `json:"provider"`
	RequestedModel    string            `json:"requestedModel"`
	ResolvedModel     string            `json:"resolvedModel"`
	RequestID         string            `json:"requestId"`
	ProviderRequestID string            `json:"providerRequestId,omitempty"`
	Usage             Usage             `json:"usage"`
	Latency           time.Duration     `json:"latency"`
	Attempts          int               `json:"attempts"`
	RawResponseHash   string            `json:"rawResponseHash"`
}

type Provider interface {
	Name() string
	Evaluate(context.Context, EvaluationRequest) (EvaluationResponse, error)
}
