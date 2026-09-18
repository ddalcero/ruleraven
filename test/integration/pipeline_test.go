//go:build integration

package integration_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ddalcero/ruleraven/internal/domain"
	storecontract "github.com/ddalcero/ruleraven/internal/store"
	mongostore "github.com/ddalcero/ruleraven/internal/store/mongo"
)

func TestMongoPipeline(t *testing.T) {
	uri := startReplicaSet(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	store := openStore(t, ctx, uri, "ruleraven_pipeline")
	defer store.Close(context.Background())

	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	incident := domain.Incident{ID: "incident-1", Key: "key-1", ClusterID: "cluster-1", Source: domain.Source{ClusterID: "cluster-1", Kind: "Pod", Name: "pod", UID: "uid-1"}, Status: domain.IncidentOpen, OpenedAt: now, UpdatedAt: now, ContentHash: "hash-1", Version: 1}
	createdIncident, created, err := store.UpsertIncident(ctx, incident, nil)
	if err != nil || !created || createdIncident.Version != 1 {
		t.Fatalf("UpsertIncident = %#v, %t, %v", createdIncident, created, err)
	}
	incident.Status = domain.IncidentResolved
	existing, created, err := store.UpsertIncident(ctx, incident, nil)
	if err != nil || created || existing.Status != domain.IncidentOpen {
		t.Fatalf("duplicate UpsertIncident mutated existing: %#v, %t, %v", existing, created, err)
	}

	t.Run("immutable snapshot concurrent upsert", func(t *testing.T) {
		snapshot := domain.Snapshot{Source: incident.Source, ObservedAt: now, ContentHash: "hash-1"}
		const workers = 20
		ids := make(chan string, workers)
		errs := make(chan error, workers)
		var wg sync.WaitGroup
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				got, _, err := store.UpsertSnapshot(ctx, incident.ID, snapshot, nil)
				if err != nil {
					errs <- err
					return
				}
				ids <- got.ID
			}()
		}
		wg.Wait()
		close(ids)
		close(errs)
		for err := range errs {
			t.Error(err)
		}
		var first string
		for id := range ids {
			if first == "" {
				first = id
			}
			if id != first {
				t.Errorf("snapshot IDs differ: %s vs %s", id, first)
			}
		}
	})

	t.Run("optimistic update", func(t *testing.T) {
		updated := incident
		updated.Status = domain.IncidentResolved
		updated.UpdatedAt = now.Add(time.Minute)
		if err := store.UpdateIncident(ctx, updated, 1, nil); err != nil {
			t.Fatal(err)
		}
		if err := store.UpdateIncident(ctx, updated, 1, nil); !errors.Is(err, storecontract.ErrConflict) {
			t.Fatalf("stale update error = %v", err)
		}
	})

	t.Run("transaction rollback and commit", func(t *testing.T) {
		updated := incident
		updated.Status = domain.IncidentOpen
		updated.UpdatedAt = now.Add(2 * time.Minute)
		evaluation := domain.Evaluation{ID: "evaluation-rollback", IncidentID: incident.ID, SnapshotHash: "hash-rollback", PolicyVersion: "policy-v1", RubricVersion: "rubric-v1", ProviderConfigHash: "provider-v1", Decision: domain.Decision{Severity: domain.SeverityWarning, Action: domain.ActionNotify, Summary: "test"}, CreatedAt: now}
		duplicate := domain.Notification{ID: "duplicate", EvaluationID: evaluation.ID, DestinationID: "ops", EventType: "incident.opened", Status: domain.NotificationPending, NextAttemptAt: now}
		err := store.Commit(ctx, storecontract.CommitRequest{Incident: updated, ExpectedVersion: 2, Evaluation: evaluation, Deliveries: []domain.Notification{duplicate, duplicate}})
		if err == nil {
			t.Fatal("Commit with duplicate delivery succeeded")
		}
		// A successful commit with the same expected version proves the incident update rolled back.
		evaluation.ID = "evaluation-1"
		evaluation.SnapshotHash = "hash-1"
		delivery := duplicate
		delivery.ID = "delivery-1"
		delivery.EvaluationID = evaluation.ID
		request := storecontract.CommitRequest{Incident: updated, ExpectedVersion: 2, Evaluation: evaluation, Deliveries: []domain.Notification{delivery}}
		if err := store.Commit(ctx, request); err != nil {
			t.Fatalf("Commit after rollback: %v", err)
		}
		if err := store.Commit(ctx, request); err != nil {
			t.Fatalf("idempotent repeated Commit: %v", err)
		}
	})

	t.Run("atomic claim lease recovery completion and reschedule", func(t *testing.T) {
		claim := storecontract.ClaimRequest{WorkerID: "worker-a", Now: now.Add(3 * time.Minute), LeaseDuration: time.Minute}
		var wg sync.WaitGroup
		results := make(chan storecontract.Delivery, 2)
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				got, err := store.ClaimDelivery(ctx, claim)
				if err == nil {
					results <- got
				} else if !errors.Is(err, storecontract.ErrNotFound) {
					t.Error(err)
				}
			}()
		}
		wg.Wait()
		close(results)
		var claimed []storecontract.Delivery
		for result := range results {
			claimed = append(claimed, result)
		}
		if len(claimed) != 1 {
			t.Fatalf("concurrent claims = %d, want 1", len(claimed))
		}
		if _, err := store.ClaimDelivery(ctx, storecontract.ClaimRequest{WorkerID: "worker-b", Now: claim.Now.Add(30 * time.Second), LeaseDuration: time.Minute}); !errors.Is(err, storecontract.ErrNotFound) {
			t.Fatalf("claim during lease = %v", err)
		}
		recovered, err := store.ClaimDelivery(ctx, storecontract.ClaimRequest{WorkerID: "worker-b", Now: claim.Now.Add(2 * time.Minute), LeaseDuration: time.Minute})
		if err != nil || recovered.LeaseOwner != "worker-b" {
			t.Fatalf("lease recovery = %#v, %v", recovered, err)
		}
		if err := store.RescheduleDelivery(ctx, storecontract.RescheduleRequest{ID: recovered.ID, WorkerID: "worker-b", Now: claim.Now.Add(2 * time.Minute), NextAttemptAt: claim.Now.Add(5 * time.Minute), Failure: storecontract.FailureRetryable}); err != nil {
			t.Fatal(err)
		}
		claimedAgain, err := store.ClaimDelivery(ctx, storecontract.ClaimRequest{WorkerID: "worker-c", Now: claim.Now.Add(6 * time.Minute), LeaseDuration: time.Minute})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.CompleteDelivery(ctx, storecontract.CompleteRequest{ID: claimedAgain.ID, WorkerID: "worker-c", CompletedAt: claim.Now.Add(6 * time.Minute)}); err != nil {
			t.Fatal(err)
		}

		updated := incident
		updated.UpdatedAt = claim.Now.Add(7 * time.Minute)
		evaluation := domain.Evaluation{ID: "evaluation-permanent", IncidentID: incident.ID, SnapshotHash: "hash-permanent", PolicyVersion: "policy-v1", RubricVersion: "rubric-v1", ProviderConfigHash: "provider-v1", Decision: domain.Decision{Severity: domain.SeverityWarning, Action: domain.ActionNotify, Summary: "permanent failure"}, CreatedAt: updated.UpdatedAt}
		delivery := domain.Notification{ID: "delivery-permanent", EvaluationID: evaluation.ID, DestinationID: "ops", EventType: "incident.updated", Status: domain.NotificationPending, NextAttemptAt: updated.UpdatedAt}
		if err := store.Commit(ctx, storecontract.CommitRequest{Incident: updated, ExpectedVersion: 3, Evaluation: evaluation, Deliveries: []domain.Notification{delivery}}); err != nil {
			t.Fatal(err)
		}
		permanent, err := store.ClaimDelivery(ctx, storecontract.ClaimRequest{WorkerID: "worker-d", Now: updated.UpdatedAt, LeaseDuration: time.Minute})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.RescheduleDelivery(ctx, storecontract.RescheduleRequest{ID: permanent.ID, WorkerID: "worker-d", Now: updated.UpdatedAt, Failure: storecontract.FailurePermanent}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ClaimDelivery(ctx, storecontract.ClaimRequest{WorkerID: "worker-e", Now: updated.UpdatedAt.Add(2 * time.Minute), LeaseDuration: time.Minute}); !errors.Is(err, storecontract.ErrNotFound) {
			t.Fatalf("permanently failed delivery was claimable: %v", err)
		}
	})
}

func TestMongoRejectsAdministrativeDatabaseBeforeConnect(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	for _, database := range []string{"", "admin", "local", "config"} {
		if _, err := mongostore.New(ctx, mongostore.Config{URI: "mongodb://203.0.113.1:27017", Database: database}); err == nil {
			t.Errorf("New(database=%q) succeeded", database)
		}
	}
}
