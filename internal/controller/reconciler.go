package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/runtime"

	"github.com/ddalcero/ruleraven/internal/decision"
	"github.com/ddalcero/ruleraven/internal/domain"
	ravenkube "github.com/ddalcero/ruleraven/internal/kube"
	"github.com/ddalcero/ruleraven/internal/provider"
	"github.com/ddalcero/ruleraven/internal/rules"
	storecontract "github.com/ddalcero/ruleraven/internal/store"
)

const (
	eventOpened   = "io.ruleraven.incident.opened"
	eventUpdated  = "io.ruleraven.incident.updated"
	eventResolved = "io.ruleraven.incident.resolved"
)

type Normalizer interface {
	Normalize(runtime.Object, time.Time) (domain.Snapshot, error)
}

type RuleEngine interface {
	Evaluate(domain.Snapshot, *domain.Incident) rules.Result
}

type IncidentStore interface {
	GetIncident(context.Context, string, string) (domain.Incident, error)
	UpsertIncident(context.Context, domain.Incident, *time.Time) (domain.Incident, bool, error)
	UpsertSnapshot(context.Context, string, domain.Snapshot, *time.Time) (storecontract.Snapshot, bool, error)
	Commit(context.Context, storecontract.CommitRequest) error
}

type Config struct {
	ClusterID          string
	Resolver           ravenkube.Resolver
	Normalizer         Normalizer
	Rules              RuleEngine
	Store              IncidentStore
	Primary            provider.Provider
	Fallback           provider.Provider
	Composer           decision.Composer
	ProviderConfigHash string
	Destinations       []string
	ReconcileTimeout   time.Duration
	EventQuietPeriod   time.Duration
	Now                func() time.Time
	NewID              func(string) string
}

type Result struct{ RequeueAfter time.Duration }

type Reconciler struct {
	clusterID          string
	resolver           ravenkube.Resolver
	normalizer         Normalizer
	rules              RuleEngine
	store              IncidentStore
	primary            provider.Provider
	fallback           provider.Provider
	composer           decision.Composer
	providerConfigHash string
	destinations       []string
	timeout            time.Duration
	eventQuietPeriod   time.Duration
	now                func() time.Time
	newID              func(string) string
}

func NewReconciler(config Config) (*Reconciler, error) {
	if strings.TrimSpace(config.ClusterID) == "" || config.Resolver == nil || config.Normalizer == nil || config.Rules == nil || config.Store == nil {
		return nil, fmt.Errorf("reconciler cluster, resolver, normalizer, rules, and store are required")
	}
	if config.Primary == nil {
		return nil, fmt.Errorf("reconciler primary provider is required")
	}
	if config.ReconcileTimeout <= 0 || config.EventQuietPeriod <= 0 || config.Now == nil || config.NewID == nil {
		return nil, fmt.Errorf("reconciler timeout, event quiet period, clock, and ID generator are required")
	}
	destinations := append([]string(nil), config.Destinations...)
	sort.Strings(destinations)
	for i, destination := range destinations {
		if strings.TrimSpace(destination) == "" || i > 0 && destination == destinations[i-1] {
			return nil, fmt.Errorf("reconciler destinations must be non-empty and unique")
		}
	}
	return &Reconciler{
		clusterID: config.ClusterID, resolver: config.Resolver, normalizer: config.Normalizer,
		rules: config.Rules, store: config.Store, primary: config.Primary, fallback: config.Fallback,
		composer: config.Composer, providerConfigHash: config.ProviderConfigHash,
		destinations: destinations, timeout: config.ReconcileTimeout, eventQuietPeriod: config.EventQuietPeriod,
		now: config.Now, newID: config.NewID,
	}, nil
}

func (r *Reconciler) Reconcile(ctx context.Context, key ravenkube.ResourceKey) (Result, error) {
	if err := key.Validate(); err != nil {
		return Result{}, err
	}
	reconcileCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	object, err := r.resolver.Resolve(reconcileCtx, key)
	if errors.Is(err, ravenkube.ErrNotFound) {
		return Result{}, r.resolveDeletion(reconcileCtx, key)
	}
	if err != nil {
		return Result{}, err
	}
	now := r.now().UTC()
	snapshot, err := r.normalizer.Normalize(object, now)
	if err != nil {
		return Result{}, fmt.Errorf("normalize Kubernetes resource: %w", err)
	}
	incidentKey := ravenkube.IncidentKey(snapshot.Source)
	previous, err := r.store.GetIncident(reconcileCtx, r.clusterID, incidentKey)
	if err != nil && !errors.Is(err, storecontract.ErrNotFound) {
		return Result{}, fmt.Errorf("get incident: %w", err)
	}
	var previousPtr *domain.Incident
	if err == nil {
		previousPtr = &previous
	}
	ruleResult := r.rules.Evaluate(snapshot, previousPtr)
	if isRecovery(ruleResult) && snapshot.Event != nil {
		snapshot.Conditions = append(snapshot.Conditions, domain.Condition{Type: "QuietPeriodElapsed", Status: "True", Reason: "EventQuietPeriod"})
		snapshot.ContentHash, err = ravenkube.ContentHash(snapshot)
		if err != nil {
			return Result{}, fmt.Errorf("fingerprint Event quiet-period resolution: %w", err)
		}
	}
	requeue := eventRequeue(snapshot, now, ruleResult, r.eventQuietPeriod)
	if previousPtr != nil && previous.ContentHash == snapshot.ContentHash && !isRecovery(ruleResult) {
		return Result{RequeueAfter: requeue}, nil
	}
	if ruleResult.Disposition == rules.DispositionNoMatch && previousPtr == nil {
		return Result{RequeueAfter: requeue}, nil
	}
	if previousPtr != nil && previous.Status == domain.IncidentResolved && ruleResult.Disposition == rules.DispositionNoMatch {
		return Result{}, nil
	}
	if err := r.persist(reconcileCtx, snapshot, previousPtr, ruleResult, now); err != nil {
		return Result{}, err
	}
	return Result{RequeueAfter: requeue}, nil
}

func (r *Reconciler) persist(ctx context.Context, snapshot domain.Snapshot, previous *domain.Incident, result rules.Result, now time.Time) error {
	incidentKey := ravenkube.IncidentKey(snapshot.Source)
	incident := domain.Incident{
		ID: r.newID("incident\x00" + incidentKey), Key: incidentKey, ClusterID: r.clusterID,
		Source: snapshot.Source, Status: domain.IncidentOpen, OpenedAt: now, UpdatedAt: now, Version: 1,
	}
	created := true
	if previous != nil {
		incident = *previous
		created = false
	} else {
		var err error
		incident, created, err = r.store.UpsertIncident(ctx, incident, nil)
		if err != nil {
			return fmt.Errorf("upsert incident: %w", err)
		}
	}
	if !created && incident.ContentHash == snapshot.ContentHash && !isRecovery(result) {
		return nil
	}
	if _, _, err := r.store.UpsertSnapshot(ctx, incident.ID, snapshot, nil); err != nil {
		return fmt.Errorf("upsert snapshot: %w", err)
	}

	decisionValue, answers := r.decide(ctx, snapshot, result, incident.ID)
	status := statusFor(result)
	incident.Source = snapshot.Source
	incident.Status = status
	incident.ContentHash = snapshot.ContentHash
	incident.UpdatedAt = now
	if status == domain.IncidentResolved {
		resolved := now
		incident.ResolvedAt = &resolved
	} else {
		incident.ResolvedAt = nil
	}
	policyVersion := result.PolicyVersion + "+" + r.composer.PolicyVersion()
	rubricVersion := result.QuestionSet
	evaluationSeed := strings.Join([]string{incident.ID, snapshot.ContentHash, policyVersion, rubricVersion, r.providerConfigHash}, "\x00")
	evaluationID := r.newID("evaluation\x00" + evaluationSeed)
	evaluation := domain.Evaluation{
		ID: evaluationID, IncidentID: incident.ID, SnapshotHash: snapshot.ContentHash,
		PolicyVersion: policyVersion, RubricVersion: rubricVersion, ProviderConfigHash: r.providerConfigHash,
		Decision: decisionValue, Answers: answers, CreatedAt: now,
	}
	eventType := eventUpdated
	if created {
		eventType = eventOpened
	}
	if status == domain.IncidentResolved {
		eventType = eventResolved
	}
	deliveries := r.deliveries(evaluationID, eventType, decisionValue, status, now)
	if err := r.store.Commit(ctx, storecontract.CommitRequest{
		Incident: incident, ExpectedVersion: incident.Version, Evaluation: evaluation, Deliveries: deliveries,
	}); err != nil {
		return fmt.Errorf("commit incident evaluation and outbox: %w", err)
	}
	return nil
}

func (r *Reconciler) decide(ctx context.Context, snapshot domain.Snapshot, result rules.Result, incidentID string) (domain.Decision, map[string]any) {
	if result.Disposition != rules.DispositionSemantic {
		return r.composer.Compose(result, nil, nil), nil
	}
	state, err := json.Marshal(snapshot)
	if err != nil {
		return r.composer.Compose(result, nil, err), nil
	}
	requestID := r.newID("request\x00" + incidentID + "\x00" + snapshot.ContentHash)
	request := provider.EvaluationRequest{State: state, Questions: decision.OperationalTriageV1Questions(), RubricVersion: result.QuestionSet, RequestID: requestID}
	response, providerErr := r.evaluateProvider(ctx, r.primary, request)
	if providerErr != nil && r.fallback != nil {
		response, providerErr = r.evaluateProvider(ctx, r.fallback, request)
	}
	composed := r.composer.Compose(result, response.Answers, providerErr)
	if providerErr != nil {
		return composed, nil
	}
	answers := make(map[string]any, len(response.Answers))
	for id, answer := range response.Answers {
		answers[id] = answer
	}
	return composed, answers
}

func (r *Reconciler) evaluateProvider(ctx context.Context, evaluator provider.Provider, request provider.EvaluationRequest) (provider.EvaluationResponse, error) {
	response, err := evaluator.Evaluate(ctx, request)
	if err != nil {
		return provider.EvaluationResponse{}, err
	}
	if err := provider.ValidateAnswers(request.Questions, response.Answers); err != nil {
		return provider.EvaluationResponse{}, err
	}
	return response, nil
}

func (r *Reconciler) resolveDeletion(ctx context.Context, key ravenkube.ResourceKey) error {
	source := key.Source(r.clusterID)
	incidentKey := ravenkube.IncidentKey(source)
	previous, err := r.store.GetIncident(ctx, r.clusterID, incidentKey)
	if errors.Is(err, storecontract.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get deleted resource incident: %w", err)
	}
	if previous.Status == domain.IncidentResolved {
		return nil
	}
	now := r.now().UTC()
	snapshot := domain.Snapshot{
		Source: source, ObservedAt: now,
		Conditions: []domain.Condition{{Type: "Deleted", Status: "True", Reason: "ResourceDeleted"}},
	}
	snapshot.ContentHash, err = ravenkube.ContentHash(snapshot)
	if err != nil {
		return fmt.Errorf("fingerprint deletion resolution: %w", err)
	}
	decisionValue := domain.Decision{
		Severity: domain.SeverityInfo, Action: domain.ActionRecord, Summary: "observed resource was deleted",
		ReasonCodes: []string{"resource_deleted"}, RuleIDs: []string{"resource-deleted"},
	}
	result := rules.Result{Disposition: rules.DispositionTerminal, Decision: &decisionValue, PolicyVersion: rules.PolicyVersion,
		Matches: []rules.Match{{RuleID: "resource-deleted", Reason: "observed resource was deleted", Severity: domain.SeverityInfo}}}
	return r.persist(ctx, snapshot, &previous, result, now)
}

func (r *Reconciler) deliveries(evaluationID, eventType string, decisionValue domain.Decision, status domain.IncidentStatus, now time.Time) []domain.Notification {
	if status != domain.IncidentResolved && decisionValue.Action != domain.ActionNotify && decisionValue.Action != domain.ActionPage {
		return nil
	}
	result := make([]domain.Notification, 0, len(r.destinations))
	for _, destination := range r.destinations {
		id := r.newID(strings.Join([]string{"delivery", evaluationID, destination, eventType}, "\x00"))
		result = append(result, domain.Notification{ID: id, EvaluationID: evaluationID, DestinationID: destination, EventType: eventType, Status: domain.NotificationPending, NextAttemptAt: now})
	}
	return result
}

func statusFor(result rules.Result) domain.IncidentStatus {
	if isRecovery(result) {
		return domain.IncidentResolved
	}
	if result.Disposition == rules.DispositionSuppress {
		return domain.IncidentSuppressed
	}
	return domain.IncidentOpen
}

func isRecovery(result rules.Result) bool {
	for _, match := range result.Matches {
		if match.RuleID == "recovered" || match.RuleID == "resource-deleted" {
			return true
		}
	}
	return false
}

func eventRequeue(snapshot domain.Snapshot, now time.Time, result rules.Result, quietPeriod time.Duration) time.Duration {
	if snapshot.Event == nil || result.Disposition != rules.DispositionSemantic || snapshot.Event.LastObserved.IsZero() {
		return 0
	}
	// The periodic resolution sweeper is authoritative. This short requeue keeps
	// quiet events prompt without putting timer logic in informer callbacks.
	remaining := snapshot.Event.LastObserved.Add(quietPeriod).Sub(now)
	if remaining <= 0 {
		return time.Millisecond
	}
	return remaining
}

func StableID(seed string) string {
	digest := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(digest[:])
}
