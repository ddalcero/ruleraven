package rules

import (
	"testing"
	"time"

	"github.com/ddalcero/ruleraven/internal/domain"
)

func TestPreviouslyOpenIncidentRecoversWhenNoRuleMatches(t *testing.T) {
	previous := &domain.Incident{Status: domain.IncidentOpen}
	result := NewEngine(Options{}).Evaluate(domain.Snapshot{Source: domain.Source{Kind: "Pod"}}, previous)
	assertSingleRule(t, result, DispositionTerminal, "recovered")
	if result.Decision == nil || result.Decision.Severity != domain.SeverityInfo || result.Decision.Action != domain.ActionRecord {
		t.Fatalf("decision = %#v, want info record", result.Decision)
	}
}

func TestResolvedIncidentDoesNotRecoverAgain(t *testing.T) {
	previous := &domain.Incident{Status: domain.IncidentResolved}
	result := NewEngine(Options{}).Evaluate(domain.Snapshot{Source: domain.Source{Kind: "Pod"}}, previous)
	if result.Disposition != DispositionNoMatch {
		t.Fatalf("result = %#v, want no match", result)
	}
}

func TestWarningEventQuietPeriodThresholds(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	engine := NewEngine(Options{Now: func() time.Time { return now }, EventQuietPeriod: 10 * time.Minute})
	previous := &domain.Incident{Status: domain.IncidentOpen}
	for _, test := range []struct {
		name string
		age  time.Duration
		rule string
		disp Disposition
	}{
		{name: "below", age: 10*time.Minute - time.Nanosecond, rule: "warning-event", disp: DispositionSemantic},
		{name: "at", age: 10 * time.Minute, rule: "recovered", disp: DispositionTerminal},
		{name: "above", age: 10*time.Minute + time.Nanosecond, rule: "recovered", disp: DispositionTerminal},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := engine.Evaluate(warningEvent("FailedMount", 9, now.Add(-test.age)), previous)
			assertSingleRule(t, result, test.disp, test.rule)
		})
	}
}
