package controller_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	"github.com/ddalcero/ruleraven/internal/controller"
	"github.com/ddalcero/ruleraven/internal/decision"
	"github.com/ddalcero/ruleraven/internal/domain"
	ravenkube "github.com/ddalcero/ruleraven/internal/kube"
	"github.com/ddalcero/ruleraven/internal/provider"
	"github.com/ddalcero/ruleraven/internal/rules"
	storecontract "github.com/ddalcero/ruleraven/internal/store"
)

var fixedNow = time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)

type fakeResolver struct {
	object runtime.Object
	err    error
}

func (r *fakeResolver) Resolve(context.Context, ravenkube.ResourceKey) (runtime.Object, error) {
	return r.object, r.err
}

type fakeProvider struct {
	mu       sync.Mutex
	calls    int
	response provider.EvaluationResponse
	err      error
}

func (p *fakeProvider) Name() string { return "fake" }
func (p *fakeProvider) Evaluate(context.Context, provider.EvaluationRequest) (provider.EvaluationResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	return p.response, p.err
}
func (p *fakeProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

type memoryStore struct {
	mu          sync.Mutex
	incidents   map[string]domain.Incident
	snapshots   map[string]storecontract.Snapshot
	evaluations map[string]domain.Evaluation
	deliveries  map[string]domain.Notification
}

func newMemoryStore() *memoryStore {
	return &memoryStore{
		incidents: make(map[string]domain.Incident), snapshots: make(map[string]storecontract.Snapshot),
		evaluations: make(map[string]domain.Evaluation), deliveries: make(map[string]domain.Notification),
	}
}
func (s *memoryStore) GetIncident(_ context.Context, clusterID, key string) (domain.Incident, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	incident, ok := s.incidents[clusterID+"/"+key]
	if !ok {
		return domain.Incident{}, storecontract.ErrNotFound
	}
	return incident, nil
}
func (s *memoryStore) ListOpenIncidents(context.Context) ([]domain.Incident, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result []domain.Incident
	for _, incident := range s.incidents {
		if incident.Status == domain.IncidentOpen {
			result = append(result, incident)
		}
	}
	return result, nil
}
func (s *memoryStore) UpsertIncident(_ context.Context, incident domain.Incident, _ *time.Time) (domain.Incident, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := incident.ClusterID + "/" + incident.Key
	if existing, ok := s.incidents[key]; ok {
		return existing, false, nil
	}
	s.incidents[key] = incident
	return incident, true, nil
}
func (s *memoryStore) UpsertSnapshot(_ context.Context, incidentID string, snapshot domain.Snapshot, _ *time.Time) (storecontract.Snapshot, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := incidentID + "/" + snapshot.ContentHash
	if existing, ok := s.snapshots[key]; ok {
		return existing, false, nil
	}
	stored := storecontract.Snapshot{ID: "snapshot-" + snapshot.ContentHash, IncidentID: incidentID, Snapshot: snapshot}
	s.snapshots[key] = stored
	return stored, true, nil
}
func (s *memoryStore) Commit(_ context.Context, request storecontract.CommitRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, duplicate := s.evaluations[request.Evaluation.ID]; duplicate {
		return nil
	}
	key := request.Incident.ClusterID + "/" + request.Incident.Key
	current := s.incidents[key]
	if current.Version != request.ExpectedVersion {
		return storecontract.ErrConflict
	}
	request.Incident.Version++
	s.incidents[key] = request.Incident
	s.evaluations[request.Evaluation.ID] = request.Evaluation
	for _, delivery := range request.Deliveries {
		s.deliveries[delivery.ID] = delivery
	}
	return nil
}
func (s *memoryStore) counts() (int, int, int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.incidents), len(s.snapshots), len(s.evaluations), len(s.deliveries)
}
func (s *memoryStore) onlyEvaluation(t *testing.T) domain.Evaluation {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, evaluation := range s.evaluations {
		return evaluation
	}
	t.Fatal("no evaluation")
	return domain.Evaluation{}
}

func failedJob() *batchv1.Job {
	backoff := int32(1)
	return &batchv1.Job{
		TypeMeta:   metav1.TypeMeta{APIVersion: "batch/v1", Kind: "Job"},
		ObjectMeta: metav1.ObjectMeta{Name: "broken", Namespace: "watched", UID: types.UID("job-uid")},
		Spec:       batchv1.JobSpec{BackoffLimit: &backoff},
		Status:     batchv1.JobStatus{Failed: 1, Conditions: []batchv1.JobCondition{{Type: batchv1.JobFailed, Status: corev1.ConditionTrue, Reason: "BackoffLimitExceeded"}}},
	}
}

func warningEvent() *corev1.Event {
	return &corev1.Event{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Event"},
		ObjectMeta: metav1.ObjectMeta{Name: "warning.1", Namespace: "watched", UID: types.UID("event-uid")},
		Type:       "Warning", Reason: "FailedMount", Message: "volume unavailable",
		LastTimestamp: metav1.NewTime(fixedNow), InvolvedObject: corev1.ObjectReference{UID: types.UID("pod-uid")},
	}
}

func operationalAnswers() map[string]provider.Answer {
	impact, immediate, transient := 2.0, 0.7, 0.1
	return map[string]provider.Answer{
		decision.QuestionIncidentFamily:             {Type: provider.QuestionTypeChoice, Choice: "infrastructure"},
		decision.QuestionOperationalImpact:          {Type: provider.QuestionTypeScore, Score: &impact, Legend: map[string]string{"0": "No current user-visible impact", "1": "Limited degradation or a working workaround", "2": "Material degradation affecting multiple users or a critical workload", "3": "Broad outage, data-loss risk, or security impact"}},
		decision.QuestionRequiresImmediateAttention: {Type: provider.QuestionTypeNoul, Noul: &immediate},
		decision.QuestionLikelyTransient:            {Type: provider.QuestionTypeNoul, Noul: &transient},
	}
}

func newReconciler(t *testing.T, resolver *fakeResolver, store *memoryStore, primary, fallback provider.Provider) *controller.Reconciler {
	t.Helper()
	return newReconcilerWithClock(t, resolver, store, primary, fallback, func() time.Time { return fixedNow })
}

func newReconcilerWithClock(t *testing.T, resolver *fakeResolver, store *memoryStore, primary, fallback provider.Provider, now func() time.Time) *controller.Reconciler {
	t.Helper()
	normalizer := ravenkube.NewNormalizer(ravenkube.NormalizerOptions{ClusterID: "cluster-a", MaxBytes: 64 << 10})
	engine := rules.NewEngine(rules.Options{
		Now: now, CrashLoopRestartThreshold: 3,
		CrashLoopCriticalThreshold: 10, MinimumAge: time.Minute, EventQuietPeriod: 15 * time.Minute,
	})
	reconciler, err := controller.NewReconciler(controller.Config{
		ClusterID: "cluster-a", Resolver: resolver, Normalizer: normalizer, Rules: engine,
		Store: store, Primary: primary, Fallback: fallback, Composer: decision.NewComposer(),
		ProviderConfigHash: "provider-config-v1", Destinations: []string{"operations"},
		ReconcileTimeout: time.Second, EventQuietPeriod: 15 * time.Minute, Now: now,
		NewID: func(seed string) string { return "id-" + seed },
	})
	if err != nil {
		t.Fatalf("NewReconciler: %v", err)
	}
	return reconciler
}

func TestFailedJobPipelineIsIdempotentAndSkipsProvider(t *testing.T) {
	store := newMemoryStore()
	providerSpy := &fakeProvider{}
	reconciler := newReconciler(t, &fakeResolver{object: failedJob()}, store, providerSpy, nil)
	key := ravenkube.ResourceKey{Kind: "Job", Namespace: "watched", Name: "broken", UID: "job-uid"}

	for range 20 {
		if _, err := reconciler.Reconcile(context.Background(), key); err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
	}

	incidents, snapshots, evaluations, deliveries := store.counts()
	if incidents != 1 || snapshots != 1 || evaluations != 1 || deliveries != 1 {
		t.Fatalf("counts = (%d,%d,%d,%d), want (1,1,1,1)", incidents, snapshots, evaluations, deliveries)
	}
	if providerSpy.callCount() != 0 {
		t.Fatalf("provider calls = %d, want 0", providerSpy.callCount())
	}
}

func TestMaterialSnapshotChangeUpdatesSameIncident(t *testing.T) {
	store := newMemoryStore()
	job := failedJob()
	resolver := &fakeResolver{object: job}
	reconciler := newReconciler(t, resolver, store, &fakeProvider{}, nil)
	key := ravenkube.ResourceKey{Kind: "Job", Namespace: "watched", Name: "broken", UID: "job-uid"}
	if _, err := reconciler.Reconcile(context.Background(), key); err != nil {
		t.Fatalf("first Reconcile: %v", err)
	}
	changed := job.DeepCopy()
	changed.Status.Failed = 2
	resolver.object = changed
	if _, err := reconciler.Reconcile(context.Background(), key); err != nil {
		t.Fatalf("changed Reconcile: %v", err)
	}
	incidents, snapshots, evaluations, deliveries := store.counts()
	if incidents != 1 || snapshots != 2 || evaluations != 2 || deliveries != 2 {
		t.Fatalf("counts = (%d,%d,%d,%d), want (1,2,2,2)", incidents, snapshots, evaluations, deliveries)
	}
}

func TestSemanticEventUsesFallbackProvider(t *testing.T) {
	store := newMemoryStore()
	primary := &fakeProvider{err: errors.New("primary unavailable")}
	fallback := &fakeProvider{response: provider.EvaluationResponse{Answers: operationalAnswers(), Provider: "fallback"}}
	reconciler := newReconciler(t, &fakeResolver{object: warningEvent()}, store, primary, fallback)

	if _, err := reconciler.Reconcile(context.Background(), ravenkube.ResourceKey{Kind: "Event", Namespace: "watched", Name: "warning.1", UID: "event-uid"}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if primary.callCount() != 1 || fallback.callCount() != 1 {
		t.Fatalf("provider calls primary=%d fallback=%d, want 1 each", primary.callCount(), fallback.callCount())
	}
	if got := store.onlyEvaluation(t).Decision.Severity; got != domain.SeverityWarning {
		t.Fatalf("severity = %q, want warning", got)
	}
}

func TestMalformedPrimaryResponseUsesFallbackProvider(t *testing.T) {
	store := newMemoryStore()
	primary := &fakeProvider{response: provider.EvaluationResponse{Answers: map[string]provider.Answer{}}}
	fallback := &fakeProvider{response: provider.EvaluationResponse{Answers: operationalAnswers(), Provider: "fallback"}}
	reconciler := newReconciler(t, &fakeResolver{object: warningEvent()}, store, primary, fallback)

	if _, err := reconciler.Reconcile(context.Background(), ravenkube.ResourceKey{Kind: "Event", Namespace: "watched", Name: "warning.1", UID: "event-uid"}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if primary.callCount() != 1 || fallback.callCount() != 1 {
		t.Fatalf("provider calls primary=%d fallback=%d, want 1 each", primary.callCount(), fallback.callCount())
	}
	if got := store.onlyEvaluation(t).Decision.Severity; got != domain.SeverityWarning {
		t.Fatalf("severity = %q, want warning", got)
	}
}

func TestProviderFailuresPersistDeterministicFallback(t *testing.T) {
	store := newMemoryStore()
	primary := &fakeProvider{err: errors.New("primary unavailable")}
	fallback := &fakeProvider{err: errors.New("fallback unavailable")}
	reconciler := newReconciler(t, &fakeResolver{object: warningEvent()}, store, primary, fallback)

	if _, err := reconciler.Reconcile(context.Background(), ravenkube.ResourceKey{Kind: "Event", Namespace: "watched", Name: "warning.1", UID: "event-uid"}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	evaluation := store.onlyEvaluation(t)
	if evaluation.Decision.Severity != domain.SeverityWarning || evaluation.Decision.Action != domain.ActionNotify || len(evaluation.Decision.ReasonCodes) == 0 || evaluation.Decision.ReasonCodes[0] != "provider_unavailable" {
		t.Fatalf("fallback decision = %#v", evaluation.Decision)
	}
}

func TestDeletedResourceResolvesOnce(t *testing.T) {
	store := newMemoryStore()
	key := ravenkube.ResourceKey{Kind: "Job", Namespace: "watched", Name: "broken", UID: "job-uid"}
	resolver := &fakeResolver{object: failedJob()}
	reconciler := newReconciler(t, resolver, store, &fakeProvider{}, nil)
	if _, err := reconciler.Reconcile(context.Background(), key); err != nil {
		t.Fatalf("open Reconcile: %v", err)
	}

	resolver.object = nil
	resolver.err = ravenkube.ErrNotFound
	for range 3 {
		if _, err := reconciler.Reconcile(context.Background(), key); err != nil {
			t.Fatalf("delete Reconcile: %v", err)
		}
	}
	_, _, evaluations, deliveries := store.counts()
	if evaluations != 2 || deliveries != 2 {
		t.Fatalf("evaluation/delivery counts = %d/%d, want 2/2", evaluations, deliveries)
	}
	incident, err := store.GetIncident(context.Background(), "cluster-a", ravenkube.IncidentKey(key.Source("cluster-a")))
	if err != nil || incident.Status != domain.IncidentResolved {
		t.Fatalf("incident = %#v, err=%v, want resolved", incident, err)
	}
}

func TestEventQuietPeriodResolvesOnce(t *testing.T) {
	store := newMemoryStore()
	now := fixedNow
	primary := &fakeProvider{response: provider.EvaluationResponse{Answers: operationalAnswers()}}
	reconciler := newReconcilerWithClock(t, &fakeResolver{object: warningEvent()}, store, primary, nil, func() time.Time { return now })
	key := ravenkube.ResourceKey{Kind: "Event", Namespace: "watched", Name: "warning.1", UID: "event-uid"}

	if result, err := reconciler.Reconcile(context.Background(), key); err != nil {
		t.Fatalf("open Reconcile: %v", err)
	} else if result.RequeueAfter != 15*time.Minute {
		t.Fatalf("requeue = %s, want 15m", result.RequeueAfter)
	}
	now = now.Add(16 * time.Minute)
	for range 3 {
		if _, err := reconciler.Reconcile(context.Background(), key); err != nil {
			t.Fatalf("quiet-period Reconcile: %v", err)
		}
	}
	_, _, evaluations, deliveries := store.counts()
	if evaluations != 2 || deliveries != 2 {
		t.Fatalf("evaluation/delivery counts = %d/%d, want 2/2", evaluations, deliveries)
	}
	incident, err := store.GetIncident(context.Background(), "cluster-a", ravenkube.IncidentKey(key.Source("cluster-a")))
	if err != nil || incident.Status != domain.IncidentResolved {
		t.Fatalf("incident = %#v, err=%v, want resolved", incident, err)
	}
}
