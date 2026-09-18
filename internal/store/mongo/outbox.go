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
)

func (s *Store) ClaimDelivery(ctx context.Context, request storecontract.ClaimRequest) (storecontract.Delivery, error) {
	if strings.TrimSpace(request.WorkerID) == "" || request.Now.IsZero() || request.LeaseDuration <= 0 {
		return storecontract.Delivery{}, fmt.Errorf("worker ID, current time, and positive lease duration are required")
	}
	filter := bson.D{{Key: "$or", Value: bson.A{
		bson.D{{Key: "status", Value: domain.NotificationPending}, {Key: "next_attempt_at", Value: bson.D{{Key: "$lte", Value: request.Now}}}},
		bson.D{{Key: "status", Value: domain.NotificationDelivering}, {Key: "lease_until", Value: bson.D{{Key: "$lte", Value: request.Now}}}},
	}}}
	leaseUntil := request.Now.Add(request.LeaseDuration)
	update := bson.D{
		{Key: "$set", Value: bson.D{{Key: "status", Value: domain.NotificationDelivering}, {Key: "lease_owner", Value: request.WorkerID}, {Key: "lease_until", Value: leaseUntil}, {Key: "updated_at", Value: request.Now}}},
		{Key: "$inc", Value: bson.D{{Key: "attempts", Value: 1}}},
	}
	findOptions := options.FindOneAndUpdate().SetSort(bson.D{{Key: "next_attempt_at", Value: 1}, {Key: "_id", Value: 1}}).SetReturnDocument(options.After)
	var document outboxDocument
	if err := s.outbox.FindOneAndUpdate(ctx, filter, update, findOptions).Decode(&document); err != nil {
		if err == drivermongo.ErrNoDocuments {
			return storecontract.Delivery{}, storecontract.ErrNotFound
		}
		return storecontract.Delivery{}, fmt.Errorf("claim outbox delivery: %w", err)
	}
	return deliveryFromDocument(document), nil
}

func (s *Store) CompleteDelivery(ctx context.Context, request storecontract.CompleteRequest) error {
	if strings.TrimSpace(request.ID) == "" || strings.TrimSpace(request.WorkerID) == "" || request.CompletedAt.IsZero() {
		return fmt.Errorf("delivery ID, worker ID, and completion time are required")
	}
	filter := bson.D{{Key: "_id", Value: request.ID}, {Key: "status", Value: domain.NotificationDelivering}, {Key: "lease_owner", Value: request.WorkerID}}
	update := bson.D{
		{Key: "$set", Value: bson.D{{Key: "status", Value: domain.NotificationDelivered}, {Key: "delivered_at", Value: request.CompletedAt}, {Key: "updated_at", Value: request.CompletedAt}, {Key: "expires_at", Value: request.ExpiresAt}}},
		{Key: "$unset", Value: bson.D{{Key: "lease_owner", Value: ""}, {Key: "lease_until", Value: ""}}},
	}
	result, err := s.outbox.UpdateOne(ctx, filter, update)
	if err != nil {
		return fmt.Errorf("complete outbox delivery: %w", err)
	}
	if result.MatchedCount == 0 {
		return storecontract.ErrConflict
	}
	return nil
}

func (s *Store) RescheduleDelivery(ctx context.Context, request storecontract.RescheduleRequest) error {
	if strings.TrimSpace(request.ID) == "" || strings.TrimSpace(request.WorkerID) == "" || request.Now.IsZero() {
		return fmt.Errorf("delivery ID, worker ID, and current time are required")
	}
	var status domain.NotificationStatus
	switch request.Failure {
	case storecontract.FailureRetryable:
		if request.NextAttemptAt.Before(request.Now) {
			return fmt.Errorf("next attempt time cannot be before current time")
		}
		status = domain.NotificationPending
	case storecontract.FailurePermanent:
		status = domain.NotificationFailed
	default:
		return fmt.Errorf("unknown failure class %q", request.Failure)
	}
	set := bson.D{{Key: "status", Value: status}, {Key: "last_failure", Value: request.Failure}, {Key: "updated_at", Value: request.Now}, {Key: "expires_at", Value: request.ExpiresAt}}
	if status == domain.NotificationPending {
		set = append(set, bson.E{Key: "next_attempt_at", Value: request.NextAttemptAt})
	}
	filter := bson.D{{Key: "_id", Value: request.ID}, {Key: "status", Value: domain.NotificationDelivering}, {Key: "lease_owner", Value: request.WorkerID}}
	update := bson.D{{Key: "$set", Value: set}, {Key: "$unset", Value: bson.D{{Key: "lease_owner", Value: ""}, {Key: "lease_until", Value: ""}}}}
	result, err := s.outbox.UpdateOne(ctx, filter, update)
	if err != nil {
		return fmt.Errorf("reschedule outbox delivery: %w", err)
	}
	if result.MatchedCount == 0 {
		return storecontract.ErrConflict
	}
	return nil
}
