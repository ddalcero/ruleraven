package rules

import (
	"testing"

	"github.com/ddalcero/ruleraven/internal/domain"
)

func TestFailedJobThresholds(t *testing.T) {
	engine := NewEngine(Options{})
	for _, test := range []struct {
		name   string
		failed int32
		want   Disposition
	}{
		{name: "below", failed: 2, want: DispositionNoMatch},
		{name: "at", failed: 3, want: DispositionTerminal},
		{name: "above", failed: 4, want: DispositionTerminal},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := engine.Evaluate(domain.Snapshot{
				Source: domain.Source{Kind: "Job"},
				Job:    &domain.JobState{Failed: test.failed, BackoffLimit: 3},
			}, nil)
			assertSingleRule(t, result, test.want, "job-failed")
			if test.want == DispositionTerminal && (result.Decision == nil || result.Decision.Severity != domain.SeverityCritical || result.Decision.Action != domain.ActionPage) {
				t.Fatalf("decision = %#v, want critical page", result.Decision)
			}
		})
	}
}

func TestFailedJobConditionIsTerminal(t *testing.T) {
	result := NewEngine(Options{}).Evaluate(domain.Snapshot{
		Source:     domain.Source{Kind: "Job"},
		Job:        &domain.JobState{Failed: 1, BackoffLimit: 6},
		Conditions: []domain.Condition{{Type: "Failed", Status: "True", Reason: "BackoffLimitExceeded"}},
	}, nil)
	assertSingleRule(t, result, DispositionTerminal, "job-failed")
}
