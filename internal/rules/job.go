package rules

import (
	"strings"

	"github.com/ddalcero/ruleraven/internal/domain"
)

func (e *Engine) failedJob(snapshot domain.Snapshot) []Match {
	if !strings.EqualFold(snapshot.Source.Kind, "Job") || snapshot.Job == nil {
		return nil
	}
	failedCondition := false
	for _, condition := range snapshot.Conditions {
		if condition.Type == "Failed" && condition.Status == "True" {
			failedCondition = true
			break
		}
	}
	threshold := snapshot.Job.BackoffLimit
	if threshold < 1 {
		threshold = 1
	}
	if !failedCondition && snapshot.Job.Failed < threshold {
		return nil
	}
	return []Match{{
		RuleID: "job-failed", Severity: domain.SeverityCritical,
		Reason: "job has failed",
	}}
}
