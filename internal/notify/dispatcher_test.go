package notify

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ddalcero/ruleraven/internal/domain"
	storecontract "github.com/ddalcero/ruleraven/internal/store"
)

type fakeOutbox struct {
	mu          sync.Mutex
	deliveries  []storecontract.Delivery
	claims      []storecontract.ClaimRequest
	completed   []storecontract.CompleteRequest
	rescheduled []storecontract.RescheduleRequest
}

func (f *fakeOutbox) ClaimDelivery(_ context.Context, request storecontract.ClaimRequest) (storecontract.Delivery, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claims = append(f.claims, request)
	if len(f.deliveries) == 0 {
		return storecontract.Delivery{}, storecontract.ErrNotFound
	}
	item := f.deliveries[0]
	f.deliveries = f.deliveries[1:]
	return item, nil
}
func (f *fakeOutbox) CompleteDelivery(_ context.Context, request storecontract.CompleteRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.completed = append(f.completed, request)
	return nil
}
func (f *fakeOutbox) RescheduleDelivery(_ context.Context, request storecontract.RescheduleRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rescheduled = append(f.rescheduled, request)
	return nil
}

type outcomeNotifier struct {
	name   string
	err    error
	events []Envelope
	block  <-chan struct{}
	called chan<- string
}

func (n *outcomeNotifier) Name() string { return n.name }
func (n *outcomeNotifier) Deliver(ctx context.Context, event Envelope) error {
	n.events = append(n.events, event)
	if n.called != nil {
		n.called <- n.name
	}
	if n.block != nil {
		select {
		case <-n.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return n.err
}

func TestDispatcherCompletesDeliveryAndPreservesEventID(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	outbox := &fakeOutbox{deliveries: []storecontract.Delivery{delivery("stable-event-id", "ops", 1)}}
	notifier := &outcomeNotifier{name: "ops"}
	dispatcher := newTestDispatcher(t, outbox, notifier, now)
	processed, err := dispatcher.DispatchOne(context.Background())
	if err != nil || !processed {
		t.Fatalf("DispatchOne() = %v, %v", processed, err)
	}
	if len(outbox.completed) != 1 || outbox.completed[0].ID != "stable-event-id" {
		t.Fatalf("completed = %#v", outbox.completed)
	}
	if len(notifier.events) != 1 || notifier.events[0].ID != "stable-event-id" {
		t.Fatalf("events = %#v", notifier.events)
	}
	var data map[string]string
	if err := json.Unmarshal(notifier.events[0].Data, &data); err != nil {
		t.Fatal(err)
	}
	if data["evaluationId"] != "eval-1" || data["destinationId"] != "ops" {
		t.Fatalf("data = %v", data)
	}
}

func TestDispatcherReschedulesRetryableAndExhaustsAttempts(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		attempts    int
		deliveryErr error
		want        storecontract.FailureClass
		delay       time.Duration
	}{
		{"retryable backoff", 1, &Error{Kind: "retryable", Retryable: true}, storecontract.FailureRetryable, time.Second},
		{"retry after", 2, &Error{Kind: "rate_limited", Retryable: true, RetryDelay: 7 * time.Second}, storecontract.FailureRetryable, 7 * time.Second},
		{"attempts exhausted", 3, &Error{Kind: "server", Retryable: true}, storecontract.FailurePermanent, 0},
		{"permanent", 1, &Error{Kind: "permanent"}, storecontract.FailurePermanent, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			outbox := &fakeOutbox{deliveries: []storecontract.Delivery{delivery("id", "ops", tt.attempts)}}
			dispatcher := newTestDispatcher(t, outbox, &outcomeNotifier{name: "ops", err: tt.deliveryErr}, now)
			processed, err := dispatcher.DispatchOne(context.Background())
			if err != nil || !processed {
				t.Fatalf("DispatchOne() = %v, %v", processed, err)
			}
			if len(outbox.rescheduled) != 1 || outbox.rescheduled[0].Failure != tt.want {
				t.Fatalf("rescheduled = %#v", outbox.rescheduled)
			}
			if got := outbox.rescheduled[0].NextAttemptAt.Sub(now); got != tt.delay {
				t.Fatalf("delay = %v, want %v", got, tt.delay)
			}
		})
	}
}

func TestDispatcherClaimsExpiredLeaseAtInjectedTime(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	expired := now.Add(-time.Second)
	item := delivery("recovered-id", "ops", 2)
	item.Status = domain.NotificationDelivering
	item.LeaseUntil = &expired
	outbox := &fakeOutbox{deliveries: []storecontract.Delivery{item}}
	dispatcher := newTestDispatcher(t, outbox, &outcomeNotifier{name: "ops"}, now)
	processed, err := dispatcher.DispatchOne(context.Background())
	if err != nil || !processed {
		t.Fatalf("DispatchOne() = %v, %v", processed, err)
	}
	if len(outbox.claims) != 1 || !outbox.claims[0].Now.Equal(now) || outbox.claims[0].LeaseDuration != time.Minute {
		t.Fatalf("claims = %#v", outbox.claims)
	}
}

func TestDispatcherCancellationLeavesLeaseForRecovery(t *testing.T) {
	now := time.Now()
	outbox := &fakeOutbox{deliveries: []storecontract.Delivery{delivery("id", "ops", 1)}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dispatcher := newTestDispatcher(t, outbox, &outcomeNotifier{name: "ops"}, now)
	_, err := dispatcher.DispatchOne(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	if len(outbox.completed) != 0 || len(outbox.rescheduled) != 0 {
		t.Fatal("canceled delivery was finalized")
	}
}

func TestDispatcherCancellationDuringDeliveryLeavesLeaseForRecovery(t *testing.T) {
	now := time.Now()
	outbox := &fakeOutbox{deliveries: []storecontract.Delivery{delivery("id", "ops", 1)}}
	started := make(chan string, 1)
	notifier := &outcomeNotifier{name: "ops", block: make(chan struct{}), called: started}
	dispatcher := newTestDispatcher(t, outbox, notifier, now)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := dispatcher.DispatchOne(ctx)
		done <- err
	}()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if len(outbox.completed) != 0 || len(outbox.rescheduled) != 0 {
		t.Fatal("canceled in-flight delivery was finalized")
	}
}

func TestDispatcherWorkersKeepDestinationsIndependent(t *testing.T) {
	now := time.Now()
	blocked := make(chan struct{})
	called := make(chan string, 2)
	outbox := &fakeOutbox{deliveries: []storecontract.Delivery{delivery("slow-id", "slow", 1), delivery("fast-id", "fast", 1)}}
	slow := &outcomeNotifier{name: "slow", block: blocked, called: called}
	fast := &outcomeNotifier{name: "fast", called: called}
	dispatcher, err := NewDispatcher(DispatcherConfig{Store: outbox, Notifiers: []Notifier{slow, fast}, WorkerID: "worker", Workers: 2, ClusterID: "cluster", LeaseDuration: time.Minute, MaxAttempts: 3, InitialBackoff: time.Second, MaxBackoff: time.Minute, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- dispatcher.Run(ctx) }()
	if first := <-called; first != "slow" {
		t.Fatalf("first call = %q", first)
	}
	if second := <-called; second != "fast" {
		t.Fatalf("second call = %q", second)
	}
	close(blocked)
	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v", err)
	}
}

func newTestDispatcher(t *testing.T, outbox *fakeOutbox, notifier Notifier, now time.Time) *Dispatcher {
	t.Helper()
	dispatcher, err := NewDispatcher(DispatcherConfig{Store: outbox, Notifiers: []Notifier{notifier}, WorkerID: "worker", Workers: 1, ClusterID: "cluster", LeaseDuration: time.Minute, MaxAttempts: 3, InitialBackoff: time.Second, MaxBackoff: time.Minute, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	return dispatcher
}

func delivery(id, destination string, attempts int) storecontract.Delivery {
	return storecontract.Delivery{Notification: domain.Notification{ID: id, EvaluationID: "eval-1", DestinationID: destination, EventType: "incident.opened", Status: domain.NotificationDelivering, Attempts: attempts, NextAttemptAt: time.Now()}, LeaseOwner: "worker"}
}
