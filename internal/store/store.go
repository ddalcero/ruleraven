package store

import (
	"context"
	"errors"
	"time"

	"github.com/ddalcero/ruleraven/internal/domain"
)

var (
	ErrConflict = errors.New("store: optimistic write conflict")
	ErrNotFound = errors.New("store: not found")
)

type Snapshot struct {
	ID         string
	IncidentID string
	Snapshot   domain.Snapshot
	ExpiresAt  *time.Time
}

type FailureClass string

const (
	FailureRetryable FailureClass = "retryable"
	FailurePermanent FailureClass = "permanent"
)

type Delivery struct {
	domain.Notification
	LeaseOwner  string
	LeaseUntil  *time.Time
	LastFailure FailureClass
	DeliveredAt *time.Time
	ExpiresAt   *time.Time
}

type CommitRequest struct {
	Incident        domain.Incident
	ExpectedVersion int64
	Evaluation      domain.Evaluation
	Deliveries      []domain.Notification
	ExpiresAt       *time.Time
}

type ClaimRequest struct {
	WorkerID      string
	Now           time.Time
	LeaseDuration time.Duration
}

type CompleteRequest struct {
	ID          string
	WorkerID    string
	CompletedAt time.Time
	ExpiresAt   *time.Time
}

type RescheduleRequest struct {
	ID            string
	WorkerID      string
	Now           time.Time
	NextAttemptAt time.Time
	Failure       FailureClass
	ExpiresAt     *time.Time
}

// Store is the persistence boundary used by reconciliation and delivery.
type Store interface {
	Ready(context.Context) error
	UpsertSnapshot(context.Context, string, domain.Snapshot, *time.Time) (Snapshot, bool, error)
	UpsertIncident(context.Context, domain.Incident, *time.Time) (domain.Incident, bool, error)
	UpdateIncident(context.Context, domain.Incident, int64, *time.Time) error
	Commit(context.Context, CommitRequest) error
	ClaimDelivery(context.Context, ClaimRequest) (Delivery, error)
	CompleteDelivery(context.Context, CompleteRequest) error
	RescheduleDelivery(context.Context, RescheduleRequest) error
	Close(context.Context) error
}
