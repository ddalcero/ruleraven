package mongo

import (
	"context"
	"fmt"
	"strings"

	"github.com/ddalcero/ruleraven/internal/domain"
	storecontract "github.com/ddalcero/ruleraven/internal/store"
	"go.mongodb.org/mongo-driver/bson"
	drivermongo "go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readconcern"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
)

func (s *Store) Commit(ctx context.Context, request storecontract.CommitRequest) error {
	if err := validateIncident(request.Incident); err != nil {
		return err
	}
	if request.ExpectedVersion <= 0 {
		return fmt.Errorf("expected incident version must be positive")
	}
	if strings.TrimSpace(request.Evaluation.ID) == "" || request.Evaluation.IncidentID != request.Incident.ID {
		return fmt.Errorf("evaluation ID and matching incident ID are required")
	}
	for _, delivery := range request.Deliveries {
		if strings.TrimSpace(delivery.ID) == "" || delivery.EvaluationID != request.Evaluation.ID {
			return fmt.Errorf("delivery ID and matching evaluation ID are required")
		}
	}

	session, err := s.client.StartSession()
	if err != nil {
		return fmt.Errorf("start MongoDB transaction session: %w", err)
	}
	defer session.EndSession(ctx)
	txnOptions := options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority())
	_, err = session.WithTransaction(ctx, func(transactionContext drivermongo.SessionContext) (any, error) {
		identity := bson.D{
			{Key: "incident_id", Value: request.Evaluation.IncidentID},
			{Key: "snapshot_hash", Value: request.Evaluation.SnapshotHash},
			{Key: "policy_version", Value: request.Evaluation.PolicyVersion},
			{Key: "rubric_version", Value: request.Evaluation.RubricVersion},
			{Key: "provider_config_hash", Value: request.Evaluation.ProviderConfigHash},
		}
		err := s.evaluations.FindOne(transactionContext, identity).Err()
		if err == nil {
			return nil, nil
		}
		if err != drivermongo.ErrNoDocuments {
			return nil, fmt.Errorf("check existing evaluation: %w", err)
		}

		if err := s.updateIncident(transactionContext, request.Incident, request.ExpectedVersion, request.ExpiresAt); err != nil {
			return nil, err
		}
		if _, err := s.evaluations.InsertOne(transactionContext, evaluationToDocument(request.Evaluation, request.ExpiresAt)); err != nil {
			return nil, fmt.Errorf("insert evaluation: %w", err)
		}
		if len(request.Deliveries) > 0 {
			documents := make([]any, 0, len(request.Deliveries))
			for _, delivery := range request.Deliveries {
				if delivery.Status == "" {
					delivery.Status = domain.NotificationPending
				}
				documents = append(documents, outboxToDocument(delivery, request.Incident.UpdatedAt, request.ExpiresAt))
			}
			if _, err := s.outbox.InsertMany(transactionContext, documents); err != nil {
				return nil, fmt.Errorf("insert outbox deliveries: %w", err)
			}
		}
		return nil, nil
	}, txnOptions)
	if err != nil {
		return fmt.Errorf("commit incident evaluation and outbox: %w", err)
	}
	return nil
}
