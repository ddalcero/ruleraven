package rules

import "github.com/ddalcero/ruleraven/internal/domain"

func (e *Engine) recovered(previous *domain.Incident) *Result {
	if previous == nil || previous.Status != domain.IncidentOpen {
		return nil
	}
	match := Match{RuleID: "recovered", Reason: "previously open incident no longer matches", Severity: domain.SeverityInfo}
	decision := domain.Decision{
		Severity: domain.SeverityInfo, Action: domain.ActionRecord, Summary: match.Reason,
		ReasonCodes: []string{match.RuleID}, RuleIDs: []string{match.RuleID},
	}
	return &Result{
		Matches: []Match{match}, Disposition: DispositionTerminal,
		Decision: &decision, PolicyVersion: PolicyVersion,
	}
}

func (e *Engine) eventIsQuiet(snapshot domain.Snapshot) bool {
	if snapshot.Event == nil || e.options.EventQuietPeriod <= 0 || snapshot.Event.LastObserved.IsZero() {
		return false
	}
	return !e.now().Before(snapshot.Event.LastObserved.Add(e.options.EventQuietPeriod))
}
