package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ddalcero/ruleraven/internal/domain"
)

type contractStore struct{}

func (contractStore) Ready(context.Context) error { return nil }
func (contractStore) GetIncident(context.Context, string, string) (domain.Incident, error) {
	return domain.Incident{}, ErrNotFound
}
func (contractStore) ListOpenIncidents(context.Context) ([]domain.Incident, error) { return nil, nil }
func (contractStore) UpsertSnapshot(context.Context, string, domain.Snapshot, *time.Time) (Snapshot, bool, error) {
	return Snapshot{}, false, nil
}
func (contractStore) UpsertIncident(context.Context, domain.Incident, *time.Time) (domain.Incident, bool, error) {
	return domain.Incident{}, false, nil
}
func (contractStore) UpdateIncident(context.Context, domain.Incident, int64, *time.Time) error {
	return nil
}
func (contractStore) Commit(context.Context, CommitRequest) error { return nil }
func (contractStore) ClaimDelivery(context.Context, ClaimRequest) (Delivery, error) {
	return Delivery{}, ErrNotFound
}
func (contractStore) CompleteDelivery(context.Context, CompleteRequest) error     { return nil }
func (contractStore) RescheduleDelivery(context.Context, RescheduleRequest) error { return nil }
func (contractStore) Close(context.Context) error                                 { return nil }

func TestStoreBoundary(t *testing.T) {
	var implementation Store = contractStore{}
	_, err := implementation.ClaimDelivery(context.Background(), ClaimRequest{})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("ClaimDelivery error = %v, want ErrNotFound", err)
	}
}
