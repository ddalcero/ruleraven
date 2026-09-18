package mongo

import (
	"context"
	"fmt"
	"sort"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	drivermongo "go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	incidentsCollection   = "incidents"
	snapshotsCollection   = "snapshots"
	evaluationsCollection = "evaluations"
	outboxCollection      = "outbox"
)

type indexKey struct {
	Field     string
	Direction int
}

type indexSpec struct {
	Name   string
	Keys   []indexKey
	Unique bool
	TTL    *time.Duration
}

func requiredIndexes() map[string][]indexSpec {
	zero := time.Duration(0)
	return map[string][]indexSpec{
		incidentsCollection: {
			{Name: "ux_incidents_cluster_id_incident_key", Keys: []indexKey{{"cluster_id", 1}, {"incident_key", 1}}, Unique: true},
			{Name: "ix_incidents_status_updated_at", Keys: []indexKey{{"status", 1}, {"updated_at", -1}}},
			{Name: "ttl_incidents_expires_at", Keys: []indexKey{{"expires_at", 1}}, TTL: &zero},
		},
		snapshotsCollection: {
			{Name: "ux_snapshots_incident_id_content_hash", Keys: []indexKey{{"incident_id", 1}, {"content_hash", 1}}, Unique: true},
			{Name: "ix_snapshots_incident_id_observed_at", Keys: []indexKey{{"incident_id", 1}, {"observed_at", -1}}},
			{Name: "ttl_snapshots_expires_at", Keys: []indexKey{{"expires_at", 1}}, TTL: &zero},
		},
		evaluationsCollection: {
			{Name: "ux_evaluations_identity", Keys: []indexKey{{"incident_id", 1}, {"snapshot_hash", 1}, {"policy_version", 1}, {"rubric_version", 1}, {"provider_config_hash", 1}}, Unique: true},
			{Name: "ix_evaluations_incident_id_created_at", Keys: []indexKey{{"incident_id", 1}, {"created_at", -1}}},
			{Name: "ttl_evaluations_expires_at", Keys: []indexKey{{"expires_at", 1}}, TTL: &zero},
		},
		outboxCollection: {
			{Name: "ux_outbox_evaluation_destination_event", Keys: []indexKey{{"evaluation_id", 1}, {"destination_id", 1}, {"event_type", 1}}, Unique: true},
			{Name: "ix_outbox_claim", Keys: []indexKey{{"status", 1}, {"next_attempt_at", 1}, {"lease_until", 1}}},
			{Name: "ix_outbox_evaluation_id", Keys: []indexKey{{"evaluation_id", 1}}},
			{Name: "ttl_outbox_expires_at", Keys: []indexKey{{"expires_at", 1}}, TTL: &zero},
		},
	}
}

func (s indexSpec) bsonKeys() bson.D {
	keys := make(bson.D, 0, len(s.Keys))
	for _, key := range s.Keys {
		keys = append(keys, bson.E{Key: key.Field, Value: key.Direction})
	}
	return keys
}

func (s indexSpec) model() drivermongo.IndexModel {
	opts := options.Index().SetName(s.Name)
	if s.Unique {
		opts.SetUnique(true)
	}
	if s.TTL != nil {
		opts.SetExpireAfterSeconds(int32(s.TTL.Seconds()))
	}
	return drivermongo.IndexModel{Keys: s.bsonKeys(), Options: opts}
}

type listedIndex struct {
	Name               string `bson:"name"`
	Key                bson.D `bson:"key"`
	Unique             bool   `bson:"unique,omitempty"`
	ExpireAfterSeconds *int64 `bson:"expireAfterSeconds,omitempty"`
}

func ensureIndexes(ctx context.Context, database *drivermongo.Database) error {
	collections, err := database.ListCollectionNames(ctx, bson.D{})
	if err != nil {
		return fmt.Errorf("list collections: %w", err)
	}
	exists := make(map[string]bool, len(collections))
	for _, name := range collections {
		exists[name] = true
	}

	names := make([]string, 0, len(requiredIndexes()))
	for name := range requiredIndexes() {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !exists[name] {
			if err := database.CreateCollection(ctx, name); err != nil {
				return fmt.Errorf("create collection %s: %w", name, err)
			}
		}
		if err := ensureCollectionIndexes(ctx, database.Collection(name), requiredIndexes()[name]); err != nil {
			return fmt.Errorf("collection %s: %w", name, err)
		}
	}
	return nil
}

func ensureCollectionIndexes(ctx context.Context, collection *drivermongo.Collection, required []indexSpec) error {
	cursor, err := collection.Indexes().List(ctx)
	if err != nil {
		return fmt.Errorf("list indexes: %w", err)
	}
	defer cursor.Close(ctx)
	var current []listedIndex
	if err := cursor.All(ctx, &current); err != nil {
		return fmt.Errorf("decode indexes: %w", err)
	}
	byName := make(map[string]listedIndex, len(current))
	for _, index := range current {
		byName[index.Name] = index
	}

	for _, expected := range required {
		if existing, ok := byName[expected.Name]; ok {
			if err := compareIndex(existing, expected); err != nil {
				return err
			}
			continue
		}
		for _, existing := range current {
			if existing.Name != "_id_" && equalKeys(existing.Key, expected.bsonKeys()) {
				return fmt.Errorf("required index %q exists with incompatible name %q", expected.Name, existing.Name)
			}
		}
		if _, err := collection.Indexes().CreateOne(ctx, expected.model()); err != nil {
			return fmt.Errorf("create index %q: %w", expected.Name, err)
		}
	}
	return nil
}

func compareIndex(existing listedIndex, expected indexSpec) error {
	if !equalKeys(existing.Key, expected.bsonKeys()) {
		return fmt.Errorf("index %q has incompatible keys %v", expected.Name, existing.Key)
	}
	if existing.Unique != expected.Unique {
		return fmt.Errorf("index %q unique=%t, want %t", expected.Name, existing.Unique, expected.Unique)
	}
	if expected.TTL == nil && existing.ExpireAfterSeconds != nil {
		return fmt.Errorf("index %q unexpectedly has TTL", expected.Name)
	}
	if expected.TTL != nil {
		want := int64(expected.TTL.Seconds())
		if existing.ExpireAfterSeconds == nil || *existing.ExpireAfterSeconds != want {
			return fmt.Errorf("index %q TTL is incompatible", expected.Name)
		}
	}
	return nil
}

func equalKeys(left, right bson.D) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].Key != right[i].Key || numericDirection(left[i].Value) != numericDirection(right[i].Value) {
			return false
		}
	}
	return true
}

func numericDirection(value any) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int32:
		return int64(typed)
	case int64:
		return typed
	case float64:
		return int64(typed)
	default:
		return 0
	}
}
