package rules

import (
	"reflect"
	"testing"
	"time"

	"github.com/ddalcero/ruleraven/internal/domain"
)

func TestSelectedWarningEventRoutesToSemanticEvaluation(t *testing.T) {
	engine := NewEngine(Options{})
	for _, reason := range []string{"FailedMount", "FailedAttachVolume", "FailedCreate", "FailedUpdate", "FailedDelete", "Unhealthy"} {
		t.Run(reason, func(t *testing.T) {
			result := engine.Evaluate(warningEvent(reason, 1, time.Unix(1, 0)), nil)
			if result.Disposition != DispositionSemantic || result.QuestionSet != "operational-triage-v1" || result.Decision != nil {
				t.Fatalf("result = %#v, want semantic operational triage", result)
			}
			if len(result.Matches) != 1 || result.Matches[0].RuleID != "warning-event" {
				t.Fatalf("matches = %#v, want warning-event", result.Matches)
			}
		})
	}
}

func TestUnselectedOrNormalEventDoesNotMatch(t *testing.T) {
	engine := NewEngine(Options{})
	for _, event := range []domain.Snapshot{
		warningEvent("Pulled", 1, time.Unix(1, 0)),
		func() domain.Snapshot {
			s := warningEvent("FailedMount", 1, time.Unix(1, 0))
			s.Event.Type = "Normal"
			return s
		}(),
	} {
		if result := engine.Evaluate(event, nil); result.Disposition != DispositionNoMatch {
			t.Fatalf("result = %#v, want no match", result)
		}
	}
}

func TestRepeatedWarningEventsAreEquivalent(t *testing.T) {
	engine := NewEngine(Options{})
	first := engine.Evaluate(warningEvent("FailedMount", 1, time.Unix(1, 0)), nil)
	repeated := warningEvent("FailedMount", 99, time.Unix(999, 0))
	repeated.ObservedAt = time.Unix(1000, 0)
	second := engine.Evaluate(repeated, nil)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("repeated event changed result:\nfirst:  %#v\nsecond: %#v", first, second)
	}
}

func warningEvent(reason string, count int32, lastObserved time.Time) domain.Snapshot {
	return domain.Snapshot{
		Source: domain.Source{Kind: "Event", UID: "event-1"},
		Event: &domain.EventState{
			Type: "Warning", Reason: reason, Message: "operation failed",
			RegardingUID: "pod-1", Count: count, LastObserved: lastObserved,
		},
	}
}
