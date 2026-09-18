package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ddalcero/ruleraven/internal/domain"
	ravenkube "github.com/ddalcero/ruleraven/internal/kube"
)

type OpenIncidentStore interface {
	ListOpenIncidents(context.Context) ([]domain.Incident, error)
}

type ResolutionQueue interface{ Add(ravenkube.ResourceKey) }

type ResolutionSweeper struct {
	store     OpenIncidentStore
	queue     ResolutionQueue
	clusterID string
	interval  time.Duration
}

func NewResolutionSweeper(store OpenIncidentStore, queue ResolutionQueue, clusterID string, interval time.Duration) (*ResolutionSweeper, error) {
	if store == nil || queue == nil || strings.TrimSpace(clusterID) == "" || interval <= 0 {
		return nil, fmt.Errorf("resolution sweeper store, queue, cluster ID, and positive interval are required")
	}
	return &ResolutionSweeper{store: store, queue: queue, clusterID: clusterID, interval: interval}, nil
}

func (s *ResolutionSweeper) SweepOnce(ctx context.Context) error {
	incidents, err := s.store.ListOpenIncidents(ctx)
	if err != nil {
		return fmt.Errorf("list incidents for resolution: %w", err)
	}
	for _, incident := range incidents {
		if incident.ClusterID != s.clusterID || !strings.EqualFold(incident.Source.Kind, "Event") {
			continue
		}
		s.queue.Add(ravenkube.ResourceKey{Kind: "Event", Namespace: incident.Source.Namespace, Name: incident.Source.Name, UID: incident.Source.UID})
	}
	return nil
}

func (s *ResolutionSweeper) Run(ctx context.Context) error {
	if err := s.SweepOnce(ctx); err != nil && ctx.Err() == nil {
		return err
	}
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := s.SweepOnce(ctx); err != nil {
				return err
			}
		}
	}
}
