package rules

import (
	"testing"
	"time"

	"github.com/ddalcero/ruleraven/internal/domain"
)

func TestPodUnschedulableThresholds(t *testing.T) {
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
			result := engine.Evaluate(domain.Snapshot{
				Source:     domain.Source{Kind: "Pod"},
				Conditions: []domain.Condition{{Type: "PodScheduled", Status: "False", Reason: "Unschedulable", LastTransitionTime: now.Add(-test.age)}},
			}, nil)
			assertSingleRule(t, result, test.want, "pod-unschedulable")
		})
	}
}

func TestImagePullFailureThresholds(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	engine := NewEngine(Options{Now: func() time.Time { return now }, MinimumAge: 5 * time.Minute})
	for _, reason := range []string{"ErrImagePull", "ImagePullBackOff"} {
		t.Run(reason, func(t *testing.T) {
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
					result := engine.Evaluate(domain.Snapshot{
						Source:     domain.Source{Kind: "Pod"},
						Conditions: []domain.Condition{{Type: "Ready", Status: "False", LastTransitionTime: now.Add(-test.age)}},
						Containers: []domain.ContainerState{{Name: "api", State: "waiting", Reason: reason}},
					}, nil)
					assertSingleRule(t, result, test.want, "image-pull-failure")
				})
			}
		})
	}
}

func assertSingleRule(t *testing.T, result Result, disposition Disposition, ruleID string) {
	t.Helper()
	if result.Disposition != disposition {
		t.Fatalf("disposition = %q, want %q (matches %#v)", result.Disposition, disposition, result.Matches)
	}
	if disposition == DispositionNoMatch {
		if len(result.Matches) != 0 || result.Decision != nil {
			t.Fatalf("no-match result = %#v", result)
		}
		return
	}
	if len(result.Matches) != 1 || result.Matches[0].RuleID != ruleID {
		t.Fatalf("matches = %#v, want %q", result.Matches, ruleID)
	}
}
