//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"testing"
	"time"

	mongostore "github.com/ddalcero/ruleraven/internal/store/mongo"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.mongodb.org/mongo-driver/bson"
	drivermongo "go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func TestMongoIndexes(t *testing.T) {
	uri := startReplicaSet(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	store := openStore(t, ctx, uri, "ruleraven_indexes")
	defer store.Close(context.Background())
	if err := store.Ready(ctx); err != nil {
		t.Fatalf("Ready: %v", err)
	}

	client, err := drivermongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Disconnect(context.Background())

	want := map[string]map[string]indexExpectation{
		"incidents": {
			"ux_incidents_cluster_id_incident_key": {unique: true},
			"ix_incidents_status_updated_at":       {},
			"ttl_incidents_expires_at":             {ttl: true},
		},
		"snapshots": {
			"ux_snapshots_incident_id_content_hash": {unique: true},
			"ix_snapshots_incident_id_observed_at":  {},
			"ttl_snapshots_expires_at":              {ttl: true},
		},
		"evaluations": {
			"ux_evaluations_identity":               {unique: true},
			"ix_evaluations_incident_id_created_at": {},
			"ttl_evaluations_expires_at":            {ttl: true},
		},
		"outbox": {
			"ux_outbox_evaluation_destination_event": {unique: true},
			"ix_outbox_claim":                        {},
			"ix_outbox_evaluation_id":                {},
			"ttl_outbox_expires_at":                  {ttl: true},
		},
	}
	for collection, expected := range want {
		cursor, err := client.Database("ruleraven_indexes").Collection(collection).Indexes().List(ctx)
		if err != nil {
			t.Fatalf("list %s indexes: %v", collection, err)
		}
		var indexes []struct {
			Name               string         `bson:"name"`
			Key                map[string]int `bson:"key"`
			Unique             bool           `bson:"unique,omitempty"`
			ExpireAfterSeconds *int64         `bson:"expireAfterSeconds,omitempty"`
		}
		if err := cursor.All(ctx, &indexes); err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		for _, idx := range indexes {
			expectation, required := expected[idx.Name]
			if !required {
				continue
			}
			seen[idx.Name] = true
			if idx.Unique != expectation.unique {
				t.Errorf("%s/%s unique=%t want %t", collection, idx.Name, idx.Unique, expectation.unique)
			}
			if expectation.ttl {
				if len(idx.Key) != 1 || idx.Key["expires_at"] != 1 || idx.ExpireAfterSeconds == nil || *idx.ExpireAfterSeconds != 0 {
					t.Errorf("%s/%s is not a single-field zero-second TTL: %#v", collection, idx.Name, idx)
				}
			}
		}
		for name := range expected {
			if !seen[name] {
				t.Errorf("%s missing index %s", collection, name)
			}
		}
	}

	indexes := client.Database("ruleraven_indexes").Collection("incidents").Indexes()
	if _, err := indexes.DropOne(ctx, "ix_incidents_status_updated_at"); err != nil {
		t.Fatal(err)
	}
	wrong := drivermongo.IndexModel{Keys: bson.D{{Key: "status", Value: -1}}, Options: options.Index().SetName("ix_incidents_status_updated_at")}
	if _, err := indexes.CreateOne(ctx, wrong); err != nil {
		t.Fatal(err)
	}
	if err := store.Ready(ctx); err == nil {
		t.Fatal("Ready accepted an existing named index with incompatible keys")
	}
}

type indexExpectation struct{ unique, ttl bool }

func startReplicaSet(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skipf("Docker unavailable: %v", err)
	}
	check := exec.Command("docker", "info", "--format", "{{.ServerVersion}}")
	if output, err := check.CombinedOutput(); err != nil {
		t.Skipf("Docker unavailable: %v (%s)", err, output)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image: "mongo:7.0", ExposedPorts: []string{"27017/tcp"},
			Cmd:        []string{"--replSet", "rs0", "--bind_ip_all"},
			WaitingFor: wait.ForLog("Waiting for connections").WithStartupTimeout(90 * time.Second),
		}, Started: true,
	})
	if err != nil {
		t.Fatalf("start MongoDB container after Docker readiness check: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })

	code, reader, err := container.Exec(ctx, []string{"mongosh", "--quiet", "--eval", `rs.initiate({_id:"rs0",members:[{_id:0,host:"localhost:27017"}]})`})
	if err != nil {
		t.Fatalf("init replica set: %v", err)
	}
	output, _ := io.ReadAll(reader)
	if code != 0 {
		t.Fatalf("init replica set exit %d: %s", code, output)
	}
	endpoint, err := container.Endpoint(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("mongodb://%s/?replicaSet=rs0&directConnection=true&retryWrites=true", endpoint)
}

func openStore(t *testing.T, ctx context.Context, uri, database string) *mongostore.Store {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		candidate, err := mongostore.New(ctx, mongostore.Config{URI: uri, Database: database, ConnectTimeout: 3 * time.Second})
		if err == nil {
			if err = candidate.Ready(ctx); err == nil {
				return candidate
			}
			_ = candidate.Close(context.Background())
		}
		lastErr = err
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("MongoDB replica set never became ready: %v", lastErr)
	return nil
}
