package mongo

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
)

var safeDatabaseName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

type Config struct {
	URI            string
	Database       string
	ConnectTimeout time.Duration
}

type Store struct {
	client      *mongo.Client
	database    *mongo.Database
	incidents   *mongo.Collection
	snapshots   *mongo.Collection
	evaluations *mongo.Collection
	outbox      *mongo.Collection
}

func New(ctx context.Context, config Config) (*Store, error) {
	if err := validateDatabase(config.Database); err != nil {
		return nil, err
	}
	if strings.TrimSpace(config.URI) == "" {
		return nil, fmt.Errorf("mongo URI is required")
	}
	if config.ConnectTimeout <= 0 {
		config.ConnectTimeout = 10 * time.Second
	}

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(config.URI).SetRetryWrites(true).SetConnectTimeout(config.ConnectTimeout).SetServerSelectionTimeout(config.ConnectTimeout))
	if err != nil {
		return nil, fmt.Errorf("connect MongoDB: %w", err)
	}
	database := client.Database(config.Database)
	return &Store{client: client, database: database, incidents: database.Collection(incidentsCollection), snapshots: database.Collection(snapshotsCollection), evaluations: database.Collection(evaluationsCollection), outbox: database.Collection(outboxCollection)}, nil
}

func validateDatabase(database string) error {
	trimmed := strings.TrimSpace(database)
	if trimmed == "" {
		return fmt.Errorf("MongoDB database is required")
	}
	switch strings.ToLower(trimmed) {
	case "admin", "local", "config":
		return fmt.Errorf("MongoDB database %q is administrative", database)
	}
	if trimmed != database || !safeDatabaseName.MatchString(database) {
		return fmt.Errorf("MongoDB database %q contains unsafe characters", database)
	}
	return nil
}

// Ready verifies primary connectivity and the exact required index contract.
func (s *Store) Ready(ctx context.Context) error {
	if err := s.client.Ping(ctx, readpref.Primary()); err != nil {
		return fmt.Errorf("ping MongoDB primary: %w", err)
	}
	if err := ensureIndexes(ctx, s.database); err != nil {
		return fmt.Errorf("reconcile MongoDB indexes: %w", err)
	}
	return nil
}

func (s *Store) Close(ctx context.Context) error { return s.client.Disconnect(ctx) }
