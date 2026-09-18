package mongo

import (
	"time"

	"github.com/ddalcero/ruleraven/internal/domain"
	storecontract "github.com/ddalcero/ruleraven/internal/store"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type sourceDocument struct {
	ClusterID       string                  `bson:"cluster_id"`
	APIVersion      string                  `bson:"api_version"`
	Kind            string                  `bson:"kind"`
	Namespace       string                  `bson:"namespace,omitempty"`
	Name            string                  `bson:"name"`
	UID             string                  `bson:"uid"`
	ResourceVersion string                  `bson:"resource_version,omitempty"`
	Generation      int64                   `bson:"generation,omitempty"`
	Owner           *ownerReferenceDocument `bson:"owner,omitempty"`
}

type ownerReferenceDocument struct {
	APIVersion string `bson:"api_version,omitempty"`
	Kind       string `bson:"kind"`
	Name       string `bson:"name"`
	UID        string `bson:"uid"`
}

type incidentDocument struct {
	ID          string                `bson:"_id"`
	IncidentKey string                `bson:"incident_key"`
	ClusterID   string                `bson:"cluster_id"`
	Source      sourceDocument        `bson:"source"`
	Status      domain.IncidentStatus `bson:"status"`
	OpenedAt    time.Time             `bson:"opened_at"`
	UpdatedAt   time.Time             `bson:"updated_at"`
	ResolvedAt  *time.Time            `bson:"resolved_at,omitempty"`
	ContentHash string                `bson:"content_hash"`
	Version     int64                 `bson:"version"`
	ExpiresAt   *time.Time            `bson:"expires_at,omitempty"`
}

type conditionDocument struct {
	Type               string    `bson:"type"`
	Status             string    `bson:"status"`
	Reason             string    `bson:"reason,omitempty"`
	Message            string    `bson:"message,omitempty"`
	LastTransitionTime time.Time `bson:"last_transition_time,omitempty"`
}

type containerDocument struct {
	Name         string `bson:"name"`
	Ready        bool   `bson:"ready"`
	RestartCount int32  `bson:"restart_count"`
	State        string `bson:"state"`
	Reason       string `bson:"reason,omitempty"`
	ExitCode     int32  `bson:"exit_code,omitempty"`
}

type workloadDocument struct {
	Desired, Current, Ready, Available, Updated, Unavailable int32
}

type jobDocument struct {
	Active       int32 `bson:"active"`
	Succeeded    int32 `bson:"succeeded"`
	Failed       int32 `bson:"failed"`
	BackoffLimit int32 `bson:"backoff_limit"`
}

type eventDocument struct {
	Type         string    `bson:"type"`
	Reason       string    `bson:"reason"`
	Message      string    `bson:"message,omitempty"`
	RegardingUID string    `bson:"regarding_uid,omitempty"`
	Count        int32     `bson:"count"`
	LastObserved time.Time `bson:"last_observed,omitempty"`
}

type snapshotPayloadDocument struct {
	Source      sourceDocument      `bson:"source"`
	ObservedAt  time.Time           `bson:"observed_at"`
	Labels      map[string]string   `bson:"labels,omitempty"`
	Conditions  []conditionDocument `bson:"conditions,omitempty"`
	Containers  []containerDocument `bson:"containers,omitempty"`
	Workload    *workloadDocument   `bson:"workload,omitempty"`
	Job         *jobDocument        `bson:"job,omitempty"`
	Event       *eventDocument      `bson:"event,omitempty"`
	ContentHash string              `bson:"content_hash"`
}

type snapshotDocument struct {
	ID          primitive.ObjectID      `bson:"_id,omitempty"`
	IncidentID  string                  `bson:"incident_id"`
	ContentHash string                  `bson:"content_hash"`
	ObservedAt  time.Time               `bson:"observed_at"`
	Snapshot    snapshotPayloadDocument `bson:"snapshot"`
	ExpiresAt   *time.Time              `bson:"expires_at,omitempty"`
}

type decisionDocument struct {
	Severity    domain.Severity `bson:"severity"`
	Action      domain.Action   `bson:"action"`
	Summary     string          `bson:"summary"`
	ReasonCodes []string        `bson:"reason_codes,omitempty"`
	RuleIDs     []string        `bson:"rule_ids,omitempty"`
}

type providerUsageDocument struct {
	InputTokens  int `bson:"input_tokens,omitempty"`
	OutputTokens int `bson:"output_tokens,omitempty"`
}

type providerAuditDocument struct {
	Provider              string                `bson:"provider"`
	RequestedModel        string                `bson:"requested_model"`
	ResolvedModel         string                `bson:"resolved_model,omitempty"`
	ResolvedModelHash     string                `bson:"resolved_model_hash,omitempty"`
	ProviderRequestIDHash string                `bson:"provider_request_id_hash,omitempty"`
	Usage                 providerUsageDocument `bson:"usage"`
	Latency               time.Duration         `bson:"latency"`
	Attempts              int                   `bson:"attempts"`
	RawResponseHash       string                `bson:"raw_response_hash"`
}

type evaluationDocument struct {
	ID                 string                 `bson:"_id"`
	IncidentID         string                 `bson:"incident_id"`
	SnapshotHash       string                 `bson:"snapshot_hash"`
	PolicyVersion      string                 `bson:"policy_version"`
	RubricVersion      string                 `bson:"rubric_version"`
	ProviderConfigHash string                 `bson:"provider_config_hash"`
	Decision           decisionDocument       `bson:"decision"`
	Answers            map[string]any         `bson:"answers,omitempty"`
	ProviderAudit      *providerAuditDocument `bson:"provider_audit,omitempty"`
	CreatedAt          time.Time              `bson:"created_at"`
	ExpiresAt          *time.Time             `bson:"expires_at,omitempty"`
}

type outboxDocument struct {
	ID            string                    `bson:"_id"`
	EvaluationID  string                    `bson:"evaluation_id"`
	DestinationID string                    `bson:"destination_id"`
	EventType     string                    `bson:"event_type"`
	Status        domain.NotificationStatus `bson:"status"`
	Attempts      int                       `bson:"attempts"`
	NextAttemptAt time.Time                 `bson:"next_attempt_at"`
	Payload       []byte                    `bson:"payload,omitempty"`
	LeaseOwner    string                    `bson:"lease_owner,omitempty"`
	LeaseUntil    *time.Time                `bson:"lease_until,omitempty"`
	LastFailure   string                    `bson:"last_failure,omitempty"`
	DeliveredAt   *time.Time                `bson:"delivered_at,omitempty"`
	UpdatedAt     time.Time                 `bson:"updated_at"`
	ExpiresAt     *time.Time                `bson:"expires_at,omitempty"`
}

func incidentToDocument(value domain.Incident, expiresAt *time.Time) incidentDocument {
	return incidentDocument{ID: value.ID, IncidentKey: value.Key, ClusterID: value.ClusterID, Source: sourceToDocument(value.Source), Status: value.Status, OpenedAt: value.OpenedAt, UpdatedAt: value.UpdatedAt, ResolvedAt: value.ResolvedAt, ContentHash: value.ContentHash, Version: value.Version, ExpiresAt: expiresAt}
}

func incidentFromDocument(value incidentDocument) domain.Incident {
	return domain.Incident{ID: value.ID, Key: value.IncidentKey, ClusterID: value.ClusterID, Source: sourceFromDocument(value.Source), Status: value.Status, OpenedAt: value.OpenedAt, UpdatedAt: value.UpdatedAt, ResolvedAt: value.ResolvedAt, ContentHash: value.ContentHash, Version: value.Version}
}

func sourceToDocument(value domain.Source) sourceDocument {
	result := sourceDocument{ClusterID: value.ClusterID, APIVersion: value.APIVersion, Kind: value.Kind, Namespace: value.Namespace, Name: value.Name, UID: value.UID, ResourceVersion: value.ResourceVersion, Generation: value.Generation}
	if value.Owner != nil {
		result.Owner = &ownerReferenceDocument{APIVersion: value.Owner.APIVersion, Kind: value.Owner.Kind, Name: value.Owner.Name, UID: value.Owner.UID}
	}
	return result
}

func sourceFromDocument(value sourceDocument) domain.Source {
	result := domain.Source{ClusterID: value.ClusterID, APIVersion: value.APIVersion, Kind: value.Kind, Namespace: value.Namespace, Name: value.Name, UID: value.UID, ResourceVersion: value.ResourceVersion, Generation: value.Generation}
	if value.Owner != nil {
		result.Owner = &domain.OwnerReference{APIVersion: value.Owner.APIVersion, Kind: value.Owner.Kind, Name: value.Owner.Name, UID: value.Owner.UID}
	}
	return result
}

func snapshotToDocument(incidentID string, value domain.Snapshot, expiresAt *time.Time) snapshotDocument {
	payload := snapshotPayloadDocument{Source: sourceToDocument(value.Source), ObservedAt: value.ObservedAt, Labels: value.Labels, ContentHash: value.ContentHash}
	for _, item := range value.Conditions {
		payload.Conditions = append(payload.Conditions, conditionDocument{Type: item.Type, Status: item.Status, Reason: item.Reason, Message: item.Message, LastTransitionTime: item.LastTransitionTime})
	}
	for _, item := range value.Containers {
		payload.Containers = append(payload.Containers, containerDocument{Name: item.Name, Ready: item.Ready, RestartCount: item.RestartCount, State: item.State, Reason: item.Reason, ExitCode: item.ExitCode})
	}
	if value.Workload != nil {
		payload.Workload = &workloadDocument{Desired: value.Workload.Desired, Current: value.Workload.Current, Ready: value.Workload.Ready, Available: value.Workload.Available, Updated: value.Workload.Updated, Unavailable: value.Workload.Unavailable}
	}
	if value.Job != nil {
		payload.Job = &jobDocument{Active: value.Job.Active, Succeeded: value.Job.Succeeded, Failed: value.Job.Failed, BackoffLimit: value.Job.BackoffLimit}
	}
	if value.Event != nil {
		payload.Event = &eventDocument{Type: value.Event.Type, Reason: value.Event.Reason, Message: value.Event.Message, RegardingUID: value.Event.RegardingUID, Count: value.Event.Count, LastObserved: value.Event.LastObserved}
	}
	return snapshotDocument{IncidentID: incidentID, ContentHash: value.ContentHash, ObservedAt: value.ObservedAt, Snapshot: payload, ExpiresAt: expiresAt}
}

func snapshotFromDocument(value snapshotDocument) storecontract.Snapshot {
	payload := value.Snapshot
	result := domain.Snapshot{Source: sourceFromDocument(payload.Source), ObservedAt: payload.ObservedAt, Labels: payload.Labels, ContentHash: payload.ContentHash}
	for _, item := range payload.Conditions {
		result.Conditions = append(result.Conditions, domain.Condition{Type: item.Type, Status: item.Status, Reason: item.Reason, Message: item.Message, LastTransitionTime: item.LastTransitionTime})
	}
	for _, item := range payload.Containers {
		result.Containers = append(result.Containers, domain.ContainerState{Name: item.Name, Ready: item.Ready, RestartCount: item.RestartCount, State: item.State, Reason: item.Reason, ExitCode: item.ExitCode})
	}
	if payload.Workload != nil {
		result.Workload = &domain.WorkloadState{Desired: payload.Workload.Desired, Current: payload.Workload.Current, Ready: payload.Workload.Ready, Available: payload.Workload.Available, Updated: payload.Workload.Updated, Unavailable: payload.Workload.Unavailable}
	}
	if payload.Job != nil {
		result.Job = &domain.JobState{Active: payload.Job.Active, Succeeded: payload.Job.Succeeded, Failed: payload.Job.Failed, BackoffLimit: payload.Job.BackoffLimit}
	}
	if payload.Event != nil {
		result.Event = &domain.EventState{Type: payload.Event.Type, Reason: payload.Event.Reason, Message: payload.Event.Message, RegardingUID: payload.Event.RegardingUID, Count: payload.Event.Count, LastObserved: payload.Event.LastObserved}
	}
	return storecontract.Snapshot{ID: value.ID.Hex(), IncidentID: value.IncidentID, Snapshot: result, ExpiresAt: value.ExpiresAt}
}

func evaluationToDocument(value domain.Evaluation, expiresAt *time.Time) evaluationDocument {
	document := evaluationDocument{ID: value.ID, IncidentID: value.IncidentID, SnapshotHash: value.SnapshotHash, PolicyVersion: value.PolicyVersion, RubricVersion: value.RubricVersion, ProviderConfigHash: value.ProviderConfigHash, Decision: decisionDocument{Severity: value.Decision.Severity, Action: value.Decision.Action, Summary: value.Decision.Summary, ReasonCodes: value.Decision.ReasonCodes, RuleIDs: value.Decision.RuleIDs}, Answers: value.Answers, CreatedAt: value.CreatedAt, ExpiresAt: expiresAt}
	if value.ProviderAudit != nil {
		document.ProviderAudit = &providerAuditDocument{Provider: value.ProviderAudit.Provider, RequestedModel: value.ProviderAudit.RequestedModel, ResolvedModel: value.ProviderAudit.ResolvedModel, ResolvedModelHash: value.ProviderAudit.ResolvedModelHash, ProviderRequestIDHash: value.ProviderAudit.ProviderRequestIDHash, Usage: providerUsageDocument{InputTokens: value.ProviderAudit.Usage.InputTokens, OutputTokens: value.ProviderAudit.Usage.OutputTokens}, Latency: value.ProviderAudit.Latency, Attempts: value.ProviderAudit.Attempts, RawResponseHash: value.ProviderAudit.RawResponseHash}
	}
	return document
}

func evaluationFromDocument(value evaluationDocument) domain.Evaluation {
	evaluation := domain.Evaluation{ID: value.ID, IncidentID: value.IncidentID, SnapshotHash: value.SnapshotHash, PolicyVersion: value.PolicyVersion, RubricVersion: value.RubricVersion, ProviderConfigHash: value.ProviderConfigHash, Decision: domain.Decision{Severity: value.Decision.Severity, Action: value.Decision.Action, Summary: value.Decision.Summary, ReasonCodes: value.Decision.ReasonCodes, RuleIDs: value.Decision.RuleIDs}, Answers: value.Answers, CreatedAt: value.CreatedAt}
	if value.ProviderAudit != nil {
		evaluation.ProviderAudit = &domain.ProviderAudit{Provider: value.ProviderAudit.Provider, RequestedModel: value.ProviderAudit.RequestedModel, ResolvedModel: value.ProviderAudit.ResolvedModel, ResolvedModelHash: value.ProviderAudit.ResolvedModelHash, ProviderRequestIDHash: value.ProviderAudit.ProviderRequestIDHash, Usage: domain.ProviderUsage{InputTokens: value.ProviderAudit.Usage.InputTokens, OutputTokens: value.ProviderAudit.Usage.OutputTokens}, Latency: value.ProviderAudit.Latency, Attempts: value.ProviderAudit.Attempts, RawResponseHash: value.ProviderAudit.RawResponseHash}
	}
	return evaluation
}

func outboxToDocument(value domain.Notification, now time.Time, expiresAt *time.Time) outboxDocument {
	return outboxDocument{ID: value.ID, EvaluationID: value.EvaluationID, DestinationID: value.DestinationID, EventType: value.EventType, Status: value.Status, Attempts: value.Attempts, NextAttemptAt: value.NextAttemptAt, Payload: append([]byte(nil), value.Payload...), UpdatedAt: now, ExpiresAt: expiresAt}
}

func deliveryFromDocument(value outboxDocument) storecontract.Delivery {
	return storecontract.Delivery{Notification: domain.Notification{ID: value.ID, EvaluationID: value.EvaluationID, DestinationID: value.DestinationID, EventType: value.EventType, Status: value.Status, Attempts: value.Attempts, NextAttemptAt: value.NextAttemptAt, Payload: append([]byte(nil), value.Payload...)}, LeaseOwner: value.LeaseOwner, LeaseUntil: value.LeaseUntil, LastFailure: storecontract.FailureClass(value.LastFailure), DeliveredAt: value.DeliveredAt, ExpiresAt: value.ExpiresAt}
}
