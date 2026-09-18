package rules

import (
	"strings"

	"github.com/ddalcero/ruleraven/internal/domain"
)

var semanticWarningReasons = map[string]struct{}{
	"FailedAttachVolume": {},
	"FailedCreate":       {},
	"FailedDelete":       {},
	"FailedMount":        {},
	"FailedUpdate":       {},
	"Unhealthy":          {},
}

func (e *Engine) warningEvent(snapshot domain.Snapshot) []Match {
	if !strings.EqualFold(snapshot.Source.Kind, "Event") || snapshot.Event == nil || !strings.EqualFold(snapshot.Event.Type, "Warning") || e.eventIsQuiet(snapshot) {
		return nil
	}
	if _, selected := semanticWarningReasons[snapshot.Event.Reason]; !selected {
		return nil
	}
	return []Match{{
		RuleID: "warning-event", Severity: domain.SeverityWarning,
		Reason: "selected Warning Event requires semantic triage",
	}}
}
