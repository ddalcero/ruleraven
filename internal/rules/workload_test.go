package rules

import (
	"testing"
	"time"

	"github.com/ddalcero/ruleraven/internal/domain"
)

func TestWorkloadUnavailableThresholds(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	engine := NewEngine(Options{Now: func() time.Time { return now }, MinimumAge: 5 * time.Minute})
	for _, test := range []struct {
		name string
		age  time.Duration
		want Disposition
	}{
		{name: "below", age: 5*time.Minute - time.Nanosecond, want: DispositionNoMatch},
		{name: "at", age: 5 * time.Minute, want: DispositionTerminal},
		{name: "above", age: 5*time.Minute + time.Nanosecond, want: DispositionTerminal},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := engine.Evaluate(workloadSnapshot(now.Add(-test.age), 2), nil)
			assertSingleRule(t, result, test.want, "workload-unavailable")
			if test.want == DispositionTerminal && result.Decision.Severity != domain.SeverityWarning {
				t.Fatalf("severity = %q, want warning", result.Decision.Severity)
			}
		})
	}
}

func TestWorkloadCompletelyUnavailableIsCritical(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	result := NewEngine(Options{Now: func() time.Time { return now }, MinimumAge: time.Minute}).Evaluate(workloadSnapshot(now.Add(-time.Minute), 0), nil)
	assertSingleRule(t, result, DispositionTerminal, "workload-unavailable")
	if result.Decision == nil || result.Decision.Severity != domain.SeverityCritical || result.Decision.Action != domain.ActionPage {
		t.Fatalf("decision = %#v, want critical page", result.Decision)
	}
}

func workloadSnapshot(transition time.Time, available int32) domain.Snapshot {
	return domain.Snapshot{
		Source:     domain.Source{Kind: "Deployment"},
		Conditions: []domain.Condition{{Type: "Available", Status: "False", Reason: "MinimumReplicasUnavailable", LastTransitionTime: transition}},
		Workload:   &domain.WorkloadState{Desired: 3, Current: available, Ready: available, Available: available, Updated: available, Unavailable: 3 - available},
	}
}
