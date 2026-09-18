package rules

import (
	"reflect"
	"testing"
	"time"

	"github.com/ddalcero/ruleraven/internal/domain"
)

func TestCrashLoopThresholds(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	engine := NewEngine(Options{
		Now:                        func() time.Time { return now },
		CrashLoopRestartThreshold:  3,
		CrashLoopCriticalThreshold: 10,
		MinimumAge:                 5 * time.Minute,
	})

	tests := []struct {
		name        string
		restarts    int32
		age         time.Duration
		disposition Disposition
		severity    domain.Severity
	}{
		{name: "below count", restarts: 2, age: 5 * time.Minute, disposition: DispositionNoMatch},
		{name: "below age", restarts: 3, age: 5*time.Minute - time.Nanosecond, disposition: DispositionNoMatch},
		{name: "at threshold", restarts: 3, age: 5 * time.Minute, disposition: DispositionTerminal, severity: domain.SeverityWarning},
		{name: "above threshold", restarts: 4, age: 5*time.Minute + time.Nanosecond, disposition: DispositionTerminal, severity: domain.SeverityWarning},
		{name: "at critical", restarts: 10, age: 5 * time.Minute, disposition: DispositionTerminal, severity: domain.SeverityCritical},
		{name: "above critical", restarts: 11, age: 5 * time.Minute, disposition: DispositionTerminal, severity: domain.SeverityCritical},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := crashLoopSnapshot(now.Add(-test.age), test.restarts)
			result := engine.Evaluate(snapshot, nil)
			if result.Disposition != test.disposition {
				t.Fatalf("disposition = %q, want %q", result.Disposition, test.disposition)
			}
			if test.severity == "" {
				if result.Decision != nil {
					t.Fatalf("decision = %#v, want nil", result.Decision)
				}
				return
			}
			if result.Decision == nil || result.Decision.Severity != test.severity {
				t.Fatalf("decision = %#v, want severity %q", result.Decision, test.severity)
			}
			if len(result.Matches) != 1 || result.Matches[0].RuleID != "pod-crash-loop" {
				t.Fatalf("matches = %#v, want pod-crash-loop", result.Matches)
			}
			if result.PolicyVersion != PolicyVersion {
				t.Fatalf("policy version = %q, want %q", result.PolicyVersion, PolicyVersion)
			}
		})
	}
}

func crashLoopSnapshot(transition time.Time, restarts int32) domain.Snapshot {
	return domain.Snapshot{
		Source:     domain.Source{Kind: "Pod", Namespace: "default", Name: "api", UID: "pod-1"},
		Conditions: []domain.Condition{{Type: "Ready", Status: "False", LastTransitionTime: transition}},
		Containers: []domain.ContainerState{{Name: "api", RestartCount: restarts, State: "waiting", Reason: "CrashLoopBackOff"}},
	}
}

func TestIgnoredNamespaceAndLabelsAreSuppressed(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	engine := NewEngine(Options{
		Now: func() time.Time { return now }, MinimumAge: time.Minute,
		CrashLoopRestartThreshold: 1, CrashLoopCriticalThreshold: 2,
		IgnoredNamespaces: []string{"kube-system"},
		IgnoredLabels:     []string{"ruleraven.io/ignore=true", "maintenance"},
	})

	tests := []struct {
		name      string
		namespace string
		labels    map[string]string
		reason    string
	}{
		{name: "namespace", namespace: "kube-system", reason: "ignored namespace kube-system"},
		{name: "label value", namespace: "default", labels: map[string]string{"ruleraven.io/ignore": "true"}, reason: "ignored label ruleraven.io/ignore=true"},
		{name: "label presence", namespace: "default", labels: map[string]string{"maintenance": "window-1"}, reason: "ignored label maintenance"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := crashLoopSnapshot(now.Add(-time.Hour), 99)
			snapshot.Source.Namespace = test.namespace
			snapshot.Labels = test.labels
			result := engine.Evaluate(snapshot, nil)
			if result.Disposition != DispositionSuppress || len(result.Matches) != 1 || result.Matches[0].RuleID != "ignored" || result.Matches[0].Reason != test.reason {
				t.Fatalf("result = %#v, want suppression reason %q", result, test.reason)
			}
			if result.Decision == nil || result.Decision.Action != domain.ActionSuppress {
				t.Fatalf("decision = %#v, want suppress", result.Decision)
			}
		})
	}
}

func TestMatchOrderingAndCriticalFloorAreDeterministic(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	engine := NewEngine(Options{
		Now: func() time.Time { return now }, MinimumAge: time.Minute,
		CrashLoopRestartThreshold: 3, CrashLoopCriticalThreshold: 10,
	})
	makeSnapshot := func(reverse bool) domain.Snapshot {
		conditions := []domain.Condition{
			{Type: "Ready", Status: "False", LastTransitionTime: now.Add(-time.Hour)},
			{Type: "PodScheduled", Status: "False", Reason: "Unschedulable", LastTransitionTime: now.Add(-time.Hour)},
		}
		containers := []domain.ContainerState{
			{Name: "api", State: "waiting", Reason: "CrashLoopBackOff", RestartCount: 10},
			{Name: "sidecar", State: "waiting", Reason: "ImagePullBackOff"},
		}
		if reverse {
			conditions[0], conditions[1] = conditions[1], conditions[0]
			containers[0], containers[1] = containers[1], containers[0]
		}
		return domain.Snapshot{Source: domain.Source{Kind: "Pod"}, Conditions: conditions, Containers: containers}
	}

	first := engine.Evaluate(makeSnapshot(false), nil)
	second := engine.Evaluate(makeSnapshot(true), nil)
	wantIDs := []string{"image-pull-failure", "pod-crash-loop", "pod-unschedulable"}
	for _, result := range []Result{first, second} {
		if result.Disposition != DispositionTerminal || result.QuestionSet != "" {
			t.Fatalf("result = %#v, want deterministic terminal", result)
		}
		gotIDs := make([]string, len(result.Matches))
		for i, match := range result.Matches {
			gotIDs[i] = match.RuleID
		}
		if !reflect.DeepEqual(gotIDs, wantIDs) {
			t.Fatalf("match IDs = %v, want %v", gotIDs, wantIDs)
		}
		if result.Decision == nil || result.Decision.Severity != domain.SeverityCritical || result.Decision.Action != domain.ActionPage {
			t.Fatalf("decision = %#v, want critical non-downgrade floor", result.Decision)
		}
		if !reflect.DeepEqual(result.Decision.RuleIDs, wantIDs) {
			t.Fatalf("decision rule IDs = %v, want %v", result.Decision.RuleIDs, wantIDs)
		}
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("reordering changed result:\nfirst:  %#v\nsecond: %#v", first, second)
	}
}

func TestCrashLoopMatchIsStableAcrossContainerOrder(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	engine := NewEngine(Options{
		Now: func() time.Time { return now }, MinimumAge: time.Minute,
		CrashLoopRestartThreshold: 3, CrashLoopCriticalThreshold: 10,
	})
	snapshot := crashLoopSnapshot(now.Add(-time.Hour), 10)
	snapshot.Containers = append(snapshot.Containers, domain.ContainerState{Name: "worker", State: "waiting", Reason: "CrashLoopBackOff", RestartCount: 12})
	first := engine.Evaluate(snapshot, nil)
	snapshot.Containers[0], snapshot.Containers[1] = snapshot.Containers[1], snapshot.Containers[0]
	second := engine.Evaluate(snapshot, nil)
	if len(first.Matches) != 1 || !reflect.DeepEqual(first, second) {
		t.Fatalf("container order changed aggregate match:\nfirst:  %#v\nsecond: %#v", first, second)
	}
}

func TestImagePullMatchIsStableAcrossContainerOrder(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	engine := NewEngine(Options{Now: func() time.Time { return now }, MinimumAge: time.Minute})
	snapshot := domain.Snapshot{
		Source:     domain.Source{Kind: "Pod"},
		Conditions: []domain.Condition{{Type: "Ready", Status: "False", LastTransitionTime: now.Add(-time.Hour)}},
		Containers: []domain.ContainerState{
			{Name: "api", State: "waiting", Reason: "ErrImagePull"},
			{Name: "worker", State: "waiting", Reason: "ImagePullBackOff"},
		},
	}
	first := engine.Evaluate(snapshot, nil)
	snapshot.Containers[0], snapshot.Containers[1] = snapshot.Containers[1], snapshot.Containers[0]
	second := engine.Evaluate(snapshot, nil)
	if len(first.Matches) != 1 || !reflect.DeepEqual(first, second) {
		t.Fatalf("container order changed aggregate match:\nfirst:  %#v\nsecond: %#v", first, second)
	}
}
