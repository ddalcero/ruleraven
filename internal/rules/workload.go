package rules

import (
	"strings"
	"time"

	"github.com/ddalcero/ruleraven/internal/domain"
)

func (e *Engine) workloadUnavailable(snapshot domain.Snapshot) []Match {
	if snapshot.Workload == nil || !isWorkloadKind(snapshot.Source.Kind) {
		return nil
	}
	state := snapshot.Workload
	if state.Desired <= 0 || state.Available >= state.Desired || state.Unavailable <= 0 {
		return nil
	}
	transition, ok := unavailableTransition(snapshot.Conditions)
	if !ok || e.now().Sub(transition) < e.options.MinimumAge {
		return nil
	}
	severity := domain.SeverityWarning
	if state.Available == 0 {
		severity = domain.SeverityCritical
	}
	return []Match{{
		RuleID: "workload-unavailable", Severity: severity,
		Reason: "workload has unavailable replicas",
	}}
}

func isWorkloadKind(kind string) bool {
	switch strings.ToLower(kind) {
	case "deployment", "statefulset", "daemonset":
		return true
	default:
		return false
	}
}

func unavailableTransition(conditions []domain.Condition) (time.Time, bool) {
	for _, condition := range conditions {
		if condition.Status != "False" || condition.LastTransitionTime.IsZero() {
			continue
		}
		switch condition.Type {
		case "Available", "Ready", "Progressing":
			return condition.LastTransitionTime, true
		}
	}
	return time.Time{}, false
}
