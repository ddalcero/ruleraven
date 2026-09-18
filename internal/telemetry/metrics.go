package telemetry

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Metrics is a dependency-free Prometheus collector. Its public methods accept
// only bounded operational dimensions; resource and incident identifiers are
// deliberately absent from the API.
type Metrics struct {
	mu sync.RWMutex

	reconciles          map[string]uint64
	reconcileDuration   durationValues
	queueDepth          int
	incidentTransitions map[incidentLabels]uint64
	providerRequests    map[providerLabels]uint64
	providerRetries     map[providerIdentity]uint64
	providerDuration    map[providerLabels]durationValues
	mongoOperations     map[mongoLabels]uint64
	mongoDuration       map[mongoLabels]durationValues
	outboxPending       int
	deliveryAttempts    map[deliveryLabels]uint64
}

type durationValues struct {
	Count uint64
	Sum   float64
}

type incidentLabels struct{ Status, Severity, Rule string }
type providerIdentity struct{ Provider, Model string }
type providerLabels struct{ Provider, Model, Outcome string }
type mongoLabels struct{ Operation, Outcome string }
type deliveryLabels struct{ Destination, Outcome string }

func NewMetrics() *Metrics {
	return &Metrics{
		reconciles: make(map[string]uint64), incidentTransitions: make(map[incidentLabels]uint64),
		providerRequests: make(map[providerLabels]uint64), providerRetries: make(map[providerIdentity]uint64),
		providerDuration: make(map[providerLabels]durationValues), mongoOperations: make(map[mongoLabels]uint64),
		mongoDuration: make(map[mongoLabels]durationValues), deliveryAttempts: make(map[deliveryLabels]uint64),
	}
}

func (m *Metrics) ObserveReconcile(outcome string, duration time.Duration) {
	if m == nil {
		return
	}
	outcome = normalizeOutcome(outcome)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reconciles[outcome]++
	m.reconcileDuration.Count++
	m.reconcileDuration.Sum += duration.Seconds()
}

func (m *Metrics) SetQueueDepth(depth int) {
	if m == nil {
		return
	}
	if depth < 0 {
		depth = 0
	}
	m.mu.Lock()
	m.queueDepth = depth
	m.mu.Unlock()
}

func (m *Metrics) ObserveIncidentTransition(status, severity, rule string) {
	if m == nil {
		return
	}
	labels := incidentLabels{normalizeIncidentStatus(status), normalizeSeverity(severity), normalizeRule(rule)}
	m.mu.Lock()
	m.incidentTransitions[labels]++
	m.mu.Unlock()
}

func (m *Metrics) ObserveProviderRequest(provider, model, outcome string, duration time.Duration, attempts int) {
	if m == nil {
		return
	}
	labels := providerLabels{normalizeConfigured(provider), normalizeConfigured(model), normalizeOutcome(outcome)}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.providerRequests[labels]++
	value := m.providerDuration[labels]
	value.Count++
	value.Sum += duration.Seconds()
	m.providerDuration[labels] = value
	if attempts > 1 {
		m.providerRetries[providerIdentity{labels.Provider, labels.Model}] += uint64(attempts - 1)
	}
}

func (m *Metrics) ObserveMongoOperation(operation, outcome string, duration time.Duration) {
	if m == nil {
		return
	}
	labels := mongoLabels{normalizeMongoOperation(operation), normalizeOutcome(outcome)}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mongoOperations[labels]++
	value := m.mongoDuration[labels]
	value.Count++
	value.Sum += duration.Seconds()
	m.mongoDuration[labels] = value
}

func (m *Metrics) SetOutboxPending(pending int) {
	if m == nil {
		return
	}
	if pending < 0 {
		pending = 0
	}
	m.mu.Lock()
	m.outboxPending = pending
	m.mu.Unlock()
}

func (m *Metrics) ObserveDeliveryAttempt(destination, outcome string) {
	if m == nil {
		return
	}
	labels := deliveryLabels{normalizeConfigured(destination), normalizeOutcome(outcome)}
	m.mu.Lock()
	m.deliveryAttempts[labels]++
	m.mu.Unlock()
}

func (m *Metrics) Handler() http.Handler { return http.HandlerFunc(m.serveHTTP) }

func (m *Metrics) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	m.mu.RLock()
	defer m.mu.RUnlock()
	var lines []string
	for outcome, value := range m.reconciles {
		lines = append(lines, metric("ruleraven_reconciliations_total", labels("outcome", outcome), value))
	}
	lines = append(lines,
		metricFloat("ruleraven_reconcile_duration_seconds_sum", "", m.reconcileDuration.Sum),
		metric("ruleraven_reconcile_duration_seconds_count", "", m.reconcileDuration.Count),
		metricInt("ruleraven_queue_depth", "", m.queueDepth),
		metricInt("ruleraven_outbox_pending", "", m.outboxPending),
	)
	for key, value := range m.incidentTransitions {
		lines = append(lines, metric("ruleraven_incident_transitions_total", labels("rule", key.Rule, "severity", key.Severity, "status", key.Status), value))
	}
	for key, value := range m.providerRequests {
		labelSet := labels("model", key.Model, "outcome", key.Outcome, "provider", key.Provider)
		lines = append(lines, metric("ruleraven_provider_requests_total", labelSet, value))
	}
	for key, value := range m.providerRetries {
		lines = append(lines, metric("ruleraven_provider_retries_total", labels("model", key.Model, "provider", key.Provider), value))
	}
	for key, value := range m.providerDuration {
		labelSet := labels("model", key.Model, "outcome", key.Outcome, "provider", key.Provider)
		lines = append(lines, metricFloat("ruleraven_provider_request_duration_seconds_sum", labelSet, value.Sum), metric("ruleraven_provider_request_duration_seconds_count", labelSet, value.Count))
	}
	for key, value := range m.mongoOperations {
		lines = append(lines, metric("ruleraven_mongodb_operations_total", labels("operation", key.Operation, "outcome", key.Outcome), value))
	}
	for key, value := range m.mongoDuration {
		labelSet := labels("operation", key.Operation, "outcome", key.Outcome)
		lines = append(lines, metricFloat("ruleraven_mongodb_operation_duration_seconds_sum", labelSet, value.Sum), metric("ruleraven_mongodb_operation_duration_seconds_count", labelSet, value.Count))
	}
	for key, value := range m.deliveryAttempts {
		lines = append(lines, metric("ruleraven_delivery_attempts_total", labels("destination", key.Destination, "outcome", key.Outcome), value))
	}
	sort.Strings(lines)
	for _, line := range lines {
		_, _ = fmt.Fprintln(w, line)
	}
}

func labels(values ...string) string {
	parts := make([]string, 0, len(values)/2)
	for index := 0; index < len(values); index += 2 {
		parts = append(parts, values[index]+"="+strconv.Quote(values[index+1]))
	}
	return "{" + strings.Join(parts, ",") + "}"
}
func metric(name, labelSet string, value uint64) string {
	return name + labelSet + " " + strconv.FormatUint(value, 10)
}
func metricInt(name, labelSet string, value int) string {
	return name + labelSet + " " + strconv.Itoa(value)
}
func metricFloat(name, labelSet string, value float64) string {
	return name + labelSet + " " + strconv.FormatFloat(value, 'g', -1, 64)
}

var allowedOutcomes = set("success", "error", "retryable", "permanent", "not_found", "conflict", "canceled", "timeout", "unknown")
var allowedIncidentStatuses = set("open", "resolved", "suppressed", "unknown")
var allowedSeverities = set("info", "warning", "critical", "unknown")
var allowedRules = set("ignored", "job-failed", "pod-crash-loop", "pod-unschedulable", "image-pull-failure", "workload-unavailable", "warning-event", "recovered", "resource-deleted", "none", "other")
var allowedMongoOperations = set("ready", "get_incident", "list_open_incidents", "upsert_snapshot", "upsert_incident", "update_incident", "commit", "claim_delivery", "complete_delivery", "reschedule_delivery", "count_pending", "close", "unknown")

func set(values ...string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}
func normalize(value string, allowed map[string]struct{}, fallback string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if _, ok := allowed[value]; ok {
		return value
	}
	return fallback
}
func normalizeOutcome(value string) string { return normalize(value, allowedOutcomes, "unknown") }
func normalizeIncidentStatus(value string) string {
	return normalize(value, allowedIncidentStatuses, "unknown")
}
func normalizeSeverity(value string) string { return normalize(value, allowedSeverities, "unknown") }
func normalizeRule(value string) string     { return normalize(value, allowedRules, "other") }
func normalizeMongoOperation(value string) string {
	return normalize(value, allowedMongoOperations, "unknown")
}
func normalizeConfigured(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 {
		return "unknown"
	}
	return value
}
