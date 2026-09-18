package domain

import "time"

type ProviderUsage struct {
	InputTokens  int `json:"inputTokens,omitempty"`
	OutputTokens int `json:"outputTokens,omitempty"`
}

// ProviderAudit contains credential-safe metadata about the successful
// semantic evaluation. Provider response bodies are never retained.
type ProviderAudit struct {
	Provider              string        `json:"provider"`
	RequestedModel        string        `json:"requestedModel"`
	ResolvedModel         string        `json:"resolvedModel,omitempty"`
	ResolvedModelHash     string        `json:"resolvedModelHash,omitempty"`
	ProviderRequestIDHash string        `json:"providerRequestIdHash,omitempty"`
	Usage                 ProviderUsage `json:"usage"`
	Latency               time.Duration `json:"latency"`
	Attempts              int           `json:"attempts"`
	RawResponseHash       string        `json:"rawResponseHash"`
}

type Evaluation struct {
	ID                 string         `json:"id"`
	IncidentID         string         `json:"incidentId"`
	SnapshotHash       string         `json:"snapshotHash"`
	PolicyVersion      string         `json:"policyVersion"`
	RubricVersion      string         `json:"rubricVersion,omitempty"`
	ProviderConfigHash string         `json:"providerConfigHash,omitempty"`
	Decision           Decision       `json:"decision"`
	Answers            map[string]any `json:"answers,omitempty"`
	ProviderAudit      *ProviderAudit `json:"providerAudit,omitempty"`
	CreatedAt          time.Time      `json:"createdAt"`
}
