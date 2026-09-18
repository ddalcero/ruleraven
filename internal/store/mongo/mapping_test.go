package mongo

import (
	"reflect"
	"testing"
	"time"

	"github.com/ddalcero/ruleraven/internal/domain"
	"github.com/ddalcero/ruleraven/internal/store"
	"go.mongodb.org/mongo-driver/bson"
)

func TestDomainMappingRoundTripsWithoutStoringJSONFieldNames(t *testing.T) {
	resolved := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	incident := domain.Incident{
		ID: "incident-1", Key: "key-1", ClusterID: "cluster-1",
		Source: domain.Source{ClusterID: "cluster-1", APIVersion: "v1", Kind: "Pod", Namespace: "default", Name: "broken", UID: "uid-1", ResourceVersion: "99", Generation: 3},
		Status: domain.IncidentResolved, OpenedAt: resolved.Add(-time.Hour), UpdatedAt: resolved, ResolvedAt: &resolved, ContentHash: "sha256", Version: 7,
	}
	doc := incidentToDocument(incident, nil)
	raw, err := bson.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var fields bson.M
	if err := bson.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["cluster_id"]; !ok {
		t.Fatal("cluster_id field missing")
	}
	if _, ok := fields["clusterId"]; ok {
		t.Fatal("JSON field name clusterId leaked into BSON")
	}
	got := incidentFromDocument(doc)
	if !reflect.DeepEqual(got, incident) {
		t.Fatalf("round trip = %#v, want %#v", got, incident)
	}
}

func TestDeliveryMappingIncludesLeaseAndFailureClassification(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	lease := now.Add(time.Minute)
	doc := outboxDocument{
		ID: "delivery-1", EvaluationID: "evaluation-1", DestinationID: "ops", EventType: "incident.opened",
		Status: domain.NotificationDelivering, Attempts: 2, NextAttemptAt: now, LeaseOwner: "worker-1", LeaseUntil: &lease,
		LastFailure: string(store.FailureRetryable), UpdatedAt: now,
	}
	got := deliveryFromDocument(doc)
	if got.LeaseOwner != "worker-1" || got.LeaseUntil == nil || !got.LeaseUntil.Equal(lease) || got.LastFailure != store.FailureRetryable {
		t.Fatalf("delivery mapping = %#v", got)
	}
}
