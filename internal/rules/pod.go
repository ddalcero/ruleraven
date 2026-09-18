package rules

import (
	"strings"
	"time"

	"github.com/ddalcero/ruleraven/internal/domain"
)

func (e *Engine) podCrashLoop(snapshot domain.Snapshot) []Match {
	if !strings.EqualFold(snapshot.Source.Kind, "Pod") {
		return nil
	}
	transition, ok := conditionTransition(snapshot.Conditions, "Ready", "False", "")
	if !ok || e.now().Sub(transition) < e.options.MinimumAge {
		return nil
	}
	severity := domain.Severity("")
	for _, container := range snapshot.Containers {
		if container.State != "waiting" || container.Reason != "CrashLoopBackOff" || int(container.RestartCount) < e.options.CrashLoopRestartThreshold {
			continue
		}
		candidate := domain.SeverityWarning
		if int(container.RestartCount) >= e.options.CrashLoopCriticalThreshold {
			candidate = domain.SeverityCritical
		}
		if severityRank(candidate) > severityRank(severity) {
			severity = candidate
		}
	}
	if severity == "" {
		return nil
	}
	return []Match{{
		RuleID: "pod-crash-loop", Severity: severity,
		Reason: "one or more containers are crash looping",
	}}
}

func (e *Engine) podUnschedulable(snapshot domain.Snapshot) []Match {
	if !strings.EqualFold(snapshot.Source.Kind, "Pod") {
		return nil
	}
	transition, ok := conditionTransition(snapshot.Conditions, "PodScheduled", "False", "Unschedulable")
	if !ok || e.now().Sub(transition) < e.options.MinimumAge {
		return nil
	}
	return []Match{{
		RuleID: "pod-unschedulable", Severity: domain.SeverityWarning,
		Reason: "pod has remained unschedulable",
	}}
}

func (e *Engine) imagePullFailure(snapshot domain.Snapshot) []Match {
	if !strings.EqualFold(snapshot.Source.Kind, "Pod") {
		return nil
	}
	transition, ok := conditionTransition(snapshot.Conditions, "Ready", "False", "")
	if !ok || e.now().Sub(transition) < e.options.MinimumAge {
		return nil
	}
	for _, container := range snapshot.Containers {
		if container.State == "waiting" && (container.Reason == "ErrImagePull" || container.Reason == "ImagePullBackOff") {
			return []Match{{
				RuleID: "image-pull-failure", Severity: domain.SeverityWarning,
				Reason: "one or more containers cannot pull their image",
			}}
		}
	}
	return nil
}

func conditionTransition(conditions []domain.Condition, conditionType, status, reason string) (time.Time, bool) {
	for _, condition := range conditions {
		if condition.Type == conditionType && condition.Status == status && (reason == "" || condition.Reason == reason) && !condition.LastTransitionTime.IsZero() {
			return condition.LastTransitionTime, true
		}
	}
	return time.Time{}, false
}
