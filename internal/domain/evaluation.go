package domain

import "time"

type Evaluation struct {
	ID                 string         `json:"id"`
	IncidentID         string         `json:"incidentId"`
	SnapshotHash       string         `json:"snapshotHash"`
	PolicyVersion      string         `json:"policyVersion"`
	RubricVersion      string         `json:"rubricVersion,omitempty"`
	ProviderConfigHash string         `json:"providerConfigHash,omitempty"`
	Decision           Decision       `json:"decision"`
	Answers            map[string]any `json:"answers,omitempty"`
	CreatedAt          time.Time      `json:"createdAt"`
}
