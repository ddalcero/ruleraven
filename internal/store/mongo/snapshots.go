package mongo

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ddalcero/ruleraven/internal/domain"
	storecontract "github.com/ddalcero/ruleraven/internal/store"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	drivermongo "go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func (s *Store) UpsertSnapshot(ctx context.Context, incidentID string, snapshot domain.Snapshot, expiresAt *time.Time) (storecontract.Snapshot, bool, error) {
	if strings.TrimSpace(incidentID) == "" || strings.TrimSpace(snapshot.ContentHash) == "" {
		return storecontract.Snapshot{}, false, fmt.Errorf("incident ID and snapshot content hash are required")
	}
	candidateID := primitive.NewObjectID()
	document := snapshotToDocument(incidentID, snapshot, expiresAt)
	document.ID = candidateID
	filter := bson.D{{Key: "incident_id", Value: incidentID}, {Key: "content_hash", Value: snapshot.ContentHash}}
	update := bson.D{{Key: "$setOnInsert", Value: document}}
	var persisted snapshotDocument
	err := s.snapshots.FindOneAndUpdate(ctx, filter, update, options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After)).Decode(&persisted)
	if drivermongo.IsDuplicateKeyError(err) {
		err = s.snapshots.FindOne(ctx, filter).Decode(&persisted)
	}
	if err != nil {
		return storecontract.Snapshot{}, false, fmt.Errorf("upsert immutable snapshot: %w", err)
	}
	return snapshotFromDocument(persisted), persisted.ID == candidateID, nil
}
