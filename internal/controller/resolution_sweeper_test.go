package controller_test

import (
	"context"
	"testing"
	"time"

	"github.com/ddalcero/ruleraven/internal/controller"
	"github.com/ddalcero/ruleraven/internal/domain"
	ravenkube "github.com/ddalcero/ruleraven/internal/kube"
)

type sweepQueue struct{ keys []ravenkube.ResourceKey }

func (q *sweepQueue) Add(key ravenkube.ResourceKey) { q.keys = append(q.keys, key) }

func TestResolutionSweeperEnqueuesOnlyOpenEventIncidents(t *testing.T) {
	store := newMemoryStore()
	store.incidents["cluster-a/event"] = domain.Incident{
		ID: "incident", Key: "event", ClusterID: "cluster-a", Status: domain.IncidentOpen,
		Source: domain.Source{Kind: "Event", Namespace: "watched", Name: "warning.1", UID: "event-uid"},
	}
	store.incidents["cluster-a/job"] = domain.Incident{
		ID: "job", Key: "job", ClusterID: "cluster-a", Status: domain.IncidentOpen,
		Source: domain.Source{Kind: "Job", Namespace: "watched", Name: "job", UID: "job-uid"},
	}
	store.incidents["cluster-b/event"] = domain.Incident{
		ID: "foreign", Key: "foreign", ClusterID: "cluster-b", Status: domain.IncidentOpen,
		Source: domain.Source{Kind: "Event", Namespace: "watched", Name: "foreign", UID: "foreign-uid"},
	}
	queue := &sweepQueue{}
	sweeper, err := controller.NewResolutionSweeper(store, queue, "cluster-a", time.Minute)
	if err != nil {
		t.Fatalf("NewResolutionSweeper: %v", err)
	}
	if err := sweeper.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if len(queue.keys) != 1 || queue.keys[0].Kind != "Event" || queue.keys[0].UID != "event-uid" {
		t.Fatalf("queued keys = %#v", queue.keys)
	}
}
