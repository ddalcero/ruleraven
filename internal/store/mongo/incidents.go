package mongo

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ddalcero/ruleraven/internal/domain"
	storecontract "github.com/ddalcero/ruleraven/internal/store"
	"go.mongodb.org/mongo-driver/bson"
	drivermongo "go.mongodb.org/mongo-driver/mongo"
)

func (s *Store) GetIncident(ctx context.Context, clusterID, incidentKey string) (domain.Incident, error) {
	if strings.TrimSpace(clusterID) == "" || strings.TrimSpace(incidentKey) == "" {
		return domain.Incident{}, fmt.Errorf("cluster ID and incident key are required")
	}
	var document incidentDocument
	err := s.incidents.FindOne(ctx, bson.D{{Key: "cluster_id", Value: clusterID}, {Key: "incident_key", Value: incidentKey}}).Decode(&document)
	if err == drivermongo.ErrNoDocuments {
		return domain.Incident{}, storecontract.ErrNotFound
	}
	if err != nil {
		return domain.Incident{}, fmt.Errorf("get incident: %w", err)
	}
	return incidentFromDocument(document), nil
}

func (s *Store) ListOpenIncidents(ctx context.Context) ([]domain.Incident, error) {
	cursor, err := s.incidents.Find(ctx, bson.D{{Key: "status", Value: domain.IncidentOpen}})
	if err != nil {
		return nil, fmt.Errorf("list open incidents: %w", err)
	}
	defer cursor.Close(ctx)
	var documents []incidentDocument
	if err := cursor.All(ctx, &documents); err != nil {
		return nil, fmt.Errorf("decode open incidents: %w", err)
	}
	incidents := make([]domain.Incident, 0, len(documents))
	for _, document := range documents {
		incidents = append(incidents, incidentFromDocument(document))
	}
	return incidents, nil
}

func (s *Store) UpsertIncident(ctx context.Context, incident domain.Incident, expiresAt *time.Time) (domain.Incident, bool, error) {
	if err := validateIncident(incident); err != nil {
		return domain.Incident{}, false, err
	}
	document := incidentToDocument(incident, expiresAt)
	filter := bson.D{{Key: "cluster_id", Value: incident.ClusterID}, {Key: "incident_key", Value: incident.Key}}
	result, err := s.incidents.UpdateOne(ctx, filter, bson.D{{Key: "$setOnInsert", Value: document}}, optionsUpdateUpsert)
	if drivermongo.IsDuplicateKeyError(err) {
		result, err = s.incidents.UpdateOne(ctx, filter, bson.D{{Key: "$setOnInsert", Value: document}})
	}
	if err != nil {
		return domain.Incident{}, false, fmt.Errorf("upsert incident: %w", err)
	}
	var persisted incidentDocument
	if err := s.incidents.FindOne(ctx, filter).Decode(&persisted); err != nil {
		return domain.Incident{}, false, fmt.Errorf("read upserted incident: %w", err)
	}
	return incidentFromDocument(persisted), result.UpsertedCount == 1, nil
}

func (s *Store) UpdateIncident(ctx context.Context, incident domain.Incident, expectedVersion int64, expiresAt *time.Time) error {
	if err := validateIncident(incident); err != nil {
		return err
	}
	return s.updateIncident(ctx, incident, expectedVersion, expiresAt)
}

func (s *Store) updateIncident(ctx context.Context, incident domain.Incident, expectedVersion int64, expiresAt *time.Time) error {
	set := bson.D{
		{Key: "source", Value: sourceToDocument(incident.Source)},
		{Key: "status", Value: incident.Status},
		{Key: "updated_at", Value: incident.UpdatedAt},
		{Key: "resolved_at", Value: incident.ResolvedAt},
		{Key: "content_hash", Value: incident.ContentHash},
		{Key: "expires_at", Value: expiresAt},
	}
	result, err := s.incidents.UpdateOne(ctx, bson.D{{Key: "_id", Value: incident.ID}, {Key: "version", Value: expectedVersion}}, bson.D{{Key: "$set", Value: set}, {Key: "$inc", Value: bson.D{{Key: "version", Value: 1}}}})
	if err != nil {
		return fmt.Errorf("update incident: %w", err)
	}
	if result.MatchedCount == 0 {
		return storecontract.ErrConflict
	}
	return nil
}

func validateIncident(incident domain.Incident) error {
	if strings.TrimSpace(incident.ID) == "" || strings.TrimSpace(incident.Key) == "" || strings.TrimSpace(incident.ClusterID) == "" {
		return fmt.Errorf("incident ID, key, and cluster ID are required")
	}
	if incident.Version <= 0 {
		return fmt.Errorf("incident version must be positive")
	}
	return nil
}
