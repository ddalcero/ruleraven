package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	storecontract "github.com/ddalcero/ruleraven/internal/store"
	"github.com/ddalcero/ruleraven/internal/telemetry"
)

type OutboxStore interface {
	ClaimDelivery(context.Context, storecontract.ClaimRequest) (storecontract.Delivery, error)
	CompleteDelivery(context.Context, storecontract.CompleteRequest) error
	RescheduleDelivery(context.Context, storecontract.RescheduleRequest) error
}

type DeliveryMetrics interface {
	SetOutboxPending(int)
	ObserveDeliveryAttempt(string, string)
}

type pendingCounter interface {
	CountPendingDeliveries(context.Context, time.Time) (int, error)
}

type DispatcherConfig struct {
	Store           OutboxStore
	Notifiers       []Notifier
	WorkerID        string
	Workers         int
	ClusterID       string
	LeaseDuration   time.Duration
	MaxAttempts     int
	InitialBackoff  time.Duration
	MaxBackoff      time.Duration
	DeliveredExpiry time.Duration
	FailedExpiry    time.Duration
	Now             func() time.Time
	Metrics         DeliveryMetrics
	Logger          *slog.Logger
}

type Dispatcher struct {
	store           OutboxStore
	notifiers       map[string]Notifier
	workerID        string
	workers         int
	clusterID       string
	leaseDuration   time.Duration
	maxAttempts     int
	initialBackoff  time.Duration
	maxBackoff      time.Duration
	deliveredExpiry time.Duration
	failedExpiry    time.Duration
	now             func() time.Time
	metrics         DeliveryMetrics
	logger          *slog.Logger
}

func NewDispatcher(config DispatcherConfig) (*Dispatcher, error) {
	if config.Store == nil {
		return nil, fmt.Errorf("dispatcher store is required")
	}
	if strings.TrimSpace(config.WorkerID) == "" || strings.TrimSpace(config.ClusterID) == "" {
		return nil, fmt.Errorf("dispatcher worker and cluster IDs are required")
	}
	if config.Workers <= 0 || config.LeaseDuration <= 0 || config.MaxAttempts <= 0 || config.InitialBackoff <= 0 || config.MaxBackoff < config.InitialBackoff {
		return nil, fmt.Errorf("dispatcher workers, lease, attempts, and backoff must be positive and consistent")
	}
	if config.DeliveredExpiry < 0 || config.FailedExpiry < 0 {
		return nil, fmt.Errorf("dispatcher expiry durations must not be negative")
	}
	if config.Now == nil {
		return nil, fmt.Errorf("dispatcher clock is required")
	}
	notifiers := make(map[string]Notifier, len(config.Notifiers))
	for _, notifier := range config.Notifiers {
		if notifier == nil || strings.TrimSpace(notifier.Name()) == "" {
			return nil, fmt.Errorf("dispatcher notifier and name are required")
		}
		if _, duplicate := notifiers[notifier.Name()]; duplicate {
			return nil, fmt.Errorf("duplicate notifier destination %q", notifier.Name())
		}
		notifiers[notifier.Name()] = notifier
	}
	return &Dispatcher{
		store: config.Store, notifiers: notifiers, workerID: config.WorkerID,
		workers: config.Workers, clusterID: config.ClusterID, leaseDuration: config.LeaseDuration,
		maxAttempts: config.MaxAttempts, initialBackoff: config.InitialBackoff,
		maxBackoff: config.MaxBackoff, deliveredExpiry: config.DeliveredExpiry,
		failedExpiry: config.FailedExpiry, now: config.Now, metrics: config.Metrics, logger: config.Logger,
	}, nil
}

// DispatchOne claims and finalizes at most one delivery. A false result means no
// eligible pending or expired-lease delivery was available.
func (d *Dispatcher) DispatchOne(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	now := d.now().UTC()
	delivery, err := d.store.ClaimDelivery(ctx, storecontract.ClaimRequest{WorkerID: d.workerID, Now: now, LeaseDuration: d.leaseDuration})
	if errors.Is(err, storecontract.ErrNotFound) {
		d.updatePending(ctx, now)
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim notification delivery: %w", err)
	}

	notifier, found := d.notifiers[delivery.DestinationID]
	deliveryStarted := time.Now()
	var deliveryErr error
	if !found {
		deliveryErr = &Error{Kind: "unknown_destination"}
	} else {
		data := append(json.RawMessage(nil), delivery.Payload...)
		var marshalErr error
		if len(data) == 0 {
			data, marshalErr = json.Marshal(struct {
				EvaluationID  string `json:"evaluationId"`
				DestinationID string `json:"destinationId"`
			}{EvaluationID: delivery.EvaluationID, DestinationID: delivery.DestinationID})
		}
		if marshalErr != nil {
			return true, fmt.Errorf("marshal notification data: %w", marshalErr)
		}
		event, envelopeErr := NewEnvelope(delivery.ID, delivery.EventType, d.clusterID, now, data)
		if envelopeErr != nil {
			deliveryErr = &Error{Kind: "invalid_delivery"}
		} else {
			deliveryErr = notifier.Deliver(ctx, event)
		}
	}
	if deliveryErr == nil {
		request := storecontract.CompleteRequest{ID: delivery.ID, WorkerID: d.workerID, CompletedAt: d.now().UTC()}
		request.ExpiresAt = expiry(request.CompletedAt, d.deliveredExpiry)
		if err := d.store.CompleteDelivery(ctx, request); err != nil {
			return true, fmt.Errorf("complete notification delivery: %w", err)
		}
		if d.metrics != nil {
			d.metrics.ObserveDeliveryAttempt(delivery.DestinationID, "success")
			d.updatePending(ctx, request.CompletedAt)
		}
		telemetry.LogDelivery(ctx, d.logger, telemetry.DeliveryLog{Destination: delivery.DestinationID, DeliveryID: delivery.ID, Outcome: "success", Attempts: delivery.Attempts, Duration: time.Since(deliveryStarted)})
		return true, nil
	}
	if err := ctx.Err(); err != nil {
		return true, err
	}

	failure := storecontract.FailurePermanent
	delay := time.Duration(0)
	var classified *Error
	if errors.As(deliveryErr, &classified) && classified.Retryable || errors.Is(deliveryErr, context.DeadlineExceeded) {
		if delivery.Attempts < d.maxAttempts {
			failure = storecontract.FailureRetryable
			delay = d.backoff(delivery.Attempts)
			if classified != nil && classified.RetryAfter() > delay {
				delay = classified.RetryAfter()
				if delay > d.maxBackoff {
					delay = d.maxBackoff
				}
			}
		}
	}
	requestNow := d.now().UTC()
	request := storecontract.RescheduleRequest{
		ID: delivery.ID, WorkerID: d.workerID, Now: requestNow,
		NextAttemptAt: requestNow.Add(delay), Failure: failure,
	}
	if failure == storecontract.FailurePermanent {
		request.ExpiresAt = expiry(requestNow, d.failedExpiry)
	}
	if err := d.store.RescheduleDelivery(ctx, request); err != nil {
		return true, fmt.Errorf("finalize failed notification delivery: %w", err)
	}
	if d.metrics != nil {
		outcome := "permanent"
		if failure == storecontract.FailureRetryable {
			outcome = "retryable"
		}
		d.metrics.ObserveDeliveryAttempt(delivery.DestinationID, outcome)
		d.updatePending(ctx, requestNow)
	}
	logOutcome := "permanent"
	if failure == storecontract.FailureRetryable {
		logOutcome = "retryable"
	}
	telemetry.LogDelivery(ctx, d.logger, telemetry.DeliveryLog{Destination: delivery.DestinationID, DeliveryID: delivery.ID, Outcome: logOutcome, Attempts: delivery.Attempts, Duration: time.Since(deliveryStarted), Err: deliveryErr})
	return true, nil
}

func (d *Dispatcher) updatePending(ctx context.Context, now time.Time) {
	if d.metrics == nil {
		return
	}
	counter, ok := d.store.(pendingCounter)
	if !ok {
		return
	}
	pending, err := counter.CountPendingDeliveries(ctx, now)
	if err == nil {
		d.metrics.SetOutboxPending(pending)
	}
}

// Run drains currently eligible deliveries with independent workers. Callers may
// invoke Run again after the next scheduled attempt becomes due.
func (d *Dispatcher) Run(ctx context.Context) error {
	var workers sync.WaitGroup
	errorsOut := make(chan error, d.workers)
	workers.Add(d.workers)
	for range d.workers {
		go func() {
			defer workers.Done()
			for {
				processed, err := d.DispatchOne(ctx)
				if err != nil {
					errorsOut <- err
					return
				}
				if !processed {
					return
				}
			}
		}()
	}
	workers.Wait()
	close(errorsOut)
	for err := range errorsOut {
		if err != nil {
			return err
		}
	}
	return nil
}

func (d *Dispatcher) backoff(attempt int) time.Duration {
	delay := d.initialBackoff
	for n := 1; n < attempt; n++ {
		if delay >= d.maxBackoff/2 {
			return d.maxBackoff
		}
		delay *= 2
	}
	if delay > d.maxBackoff {
		return d.maxBackoff
	}
	return delay
}

func expiry(now time.Time, retention time.Duration) *time.Time {
	if retention <= 0 {
		return nil
	}
	value := now.Add(retention)
	return &value
}
