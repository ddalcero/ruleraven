package domain

import "time"

type IncidentStatus string

const (
	IncidentOpen       IncidentStatus = "open"
	IncidentResolved   IncidentStatus = "resolved"
	IncidentSuppressed IncidentStatus = "suppressed"
)

type Incident struct {
	ID          string         `json:"id"`
	Key         string         `json:"key"`
	ClusterID   string         `json:"clusterId"`
	Source      Source         `json:"source"`
	Status      IncidentStatus `json:"status"`
	OpenedAt    time.Time      `json:"openedAt"`
	UpdatedAt   time.Time      `json:"updatedAt"`
	ResolvedAt  *time.Time     `json:"resolvedAt,omitempty"`
	ContentHash string         `json:"contentHash"`
	Version     int64          `json:"version"`
}
