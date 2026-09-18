package mongo

import (
	"reflect"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
)

func TestRequiredIndexesAreExactAndTTLIndexesAreSingleField(t *testing.T) {
	want := map[string][]string{
		"incidents":   {"ux_incidents_cluster_id_incident_key", "ix_incidents_status_updated_at", "ttl_incidents_expires_at"},
		"snapshots":   {"ux_snapshots_incident_id_content_hash", "ix_snapshots_incident_id_observed_at", "ttl_snapshots_expires_at"},
		"evaluations": {"ux_evaluations_identity", "ix_evaluations_incident_id_created_at", "ttl_evaluations_expires_at"},
		"outbox":      {"ux_outbox_evaluation_destination_event", "ix_outbox_claim", "ix_outbox_evaluation_id", "ttl_outbox_expires_at"},
	}

	got := make(map[string][]string)
	for collection, specs := range requiredIndexes() {
		for _, spec := range specs {
			got[collection] = append(got[collection], spec.Name)
			if spec.TTL != nil {
				if len(spec.Keys) != 1 || spec.Keys[0].Field != "expires_at" || spec.Keys[0].Direction != 1 {
					t.Fatalf("TTL index %s must be ascending and single-field on expires_at: %#v", spec.Name, spec.Keys)
				}
				if *spec.TTL != 0*time.Second {
					t.Fatalf("TTL index %s expires after %s, want 0", spec.Name, *spec.TTL)
				}
			}
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("index names = %#v, want %#v", got, want)
	}

	assertIndex(t, "incidents", "ux_incidents_cluster_id_incident_key", bson.D{{Key: "cluster_id", Value: 1}, {Key: "incident_key", Value: 1}}, true)
	assertIndex(t, "snapshots", "ux_snapshots_incident_id_content_hash", bson.D{{Key: "incident_id", Value: 1}, {Key: "content_hash", Value: 1}}, true)
	assertIndex(t, "evaluations", "ux_evaluations_identity", bson.D{{Key: "incident_id", Value: 1}, {Key: "snapshot_hash", Value: 1}, {Key: "policy_version", Value: 1}, {Key: "rubric_version", Value: 1}, {Key: "provider_config_hash", Value: 1}}, true)
	assertIndex(t, "outbox", "ux_outbox_evaluation_destination_event", bson.D{{Key: "evaluation_id", Value: 1}, {Key: "destination_id", Value: 1}, {Key: "event_type", Value: 1}}, true)
}

func assertIndex(t *testing.T, collection, name string, keys bson.D, unique bool) {
	t.Helper()
	for _, spec := range requiredIndexes()[collection] {
		if spec.Name == name {
			if !reflect.DeepEqual(spec.bsonKeys(), keys) || spec.Unique != unique {
				t.Fatalf("%s = keys %#v unique %t, want keys %#v unique %t", name, spec.bsonKeys(), spec.Unique, keys, unique)
			}
			return
		}
	}
	t.Fatalf("missing index %s", name)
}

func TestValidateDatabaseRejectsUnsafeNames(t *testing.T) {
	for _, database := range []string{"", " ", "admin", "ADMIN", "local", "config", "bad/name", "bad.name", "bad name"} {
		t.Run(database, func(t *testing.T) {
			if err := validateDatabase(database); err == nil {
				t.Fatalf("validateDatabase(%q) succeeded", database)
			}
		})
	}
	if err := validateDatabase("ruleraven_test"); err != nil {
		t.Fatalf("validateDatabase(valid) = %v", err)
	}
}
