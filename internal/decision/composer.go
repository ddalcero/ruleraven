package decision

import (
	"fmt"
	"sort"

	"github.com/ddalcero/ruleraven/internal/domain"
	"github.com/ddalcero/ruleraven/internal/provider"
	"github.com/ddalcero/ruleraven/internal/rules"
)

const CompositionPolicyVersion = "operational-composer-v1"

type Thresholds struct {
	PolicyVersion              string
	WarningImpact              float64
	CriticalImpact             float64
	WarningImmediateAttention  float64
	CriticalImmediateAttention float64
	LikelyTransientRecord      float64
}

type Composer struct {
	thresholds Thresholds
	fallback   domain.Decision
}

func NewComposer() Composer {
	return Composer{
		thresholds: Thresholds{
			PolicyVersion:              CompositionPolicyVersion,
			WarningImpact:              1,
			CriticalImpact:             2.5,
			WarningImmediateAttention:  0.5,
			CriticalImmediateAttention: 0.85,
			LikelyTransientRecord:      0.7,
		},
		fallback: domain.Decision{
			Severity:    domain.SeverityWarning,
			Action:      domain.ActionNotify,
			Summary:     "Provider evidence unavailable; manual triage required",
			ReasonCodes: []string{"provider_unavailable"},
		},
	}
}

func (c Composer) PolicyVersion() string {
	return c.thresholds.PolicyVersion
}

// Compose maps validated provider evidence to policy. Any provider error,
// unknown rubric, or malformed answer fails closed to the configured fallback.
// A deterministic decision on the rule result is always treated as a floor.
func (c Composer) Compose(result rules.Result, answers map[string]provider.Answer, providerErr error) domain.Decision {
	if result.Disposition != rules.DispositionSemantic && result.Decision != nil {
		return cloneDecision(*result.Decision)
	}

	var composed domain.Decision
	if providerErr != nil || result.QuestionSet != OperationalTriageV1 {
		composed = cloneDecision(c.fallback)
	} else if err := provider.ValidateAnswers(OperationalTriageV1Questions(), answers); err != nil {
		composed = cloneDecision(c.fallback)
	} else {
		composed = c.composeOperationalTriage(answers)
	}
	composed.RuleIDs = mergeRuleIDs(composed.RuleIDs, ruleIDs(result))
	return applyDeterministicFloor(composed, result.Decision)
}

func (c Composer) composeOperationalTriage(answers map[string]provider.Answer) domain.Decision {
	family := answers[QuestionIncidentFamily].Choice
	impact := *answers[QuestionOperationalImpact].Score
	immediate := *answers[QuestionRequiresImmediateAttention].Noul
	transient := *answers[QuestionLikelyTransient].Noul

	decision := domain.Decision{
		Severity:    domain.SeverityInfo,
		Action:      domain.ActionRecord,
		Summary:     fmt.Sprintf("Provider evidence classifies the incident as %s", family),
		ReasonCodes: []string{"provider_evidence", "incident_family_" + family},
	}
	switch {
	case impact >= c.thresholds.CriticalImpact || immediate >= c.thresholds.CriticalImmediateAttention:
		decision.Severity = domain.SeverityCritical
		decision.Action = domain.ActionPage
		decision.ReasonCodes = append(decision.ReasonCodes, "semantic_critical")
	case impact >= c.thresholds.WarningImpact || immediate >= c.thresholds.WarningImmediateAttention || transient < c.thresholds.LikelyTransientRecord:
		decision.Severity = domain.SeverityWarning
		decision.Action = domain.ActionNotify
		decision.ReasonCodes = append(decision.ReasonCodes, "semantic_warning")
	default:
		decision.ReasonCodes = append(decision.ReasonCodes, "semantic_low_impact")
	}
	return decision
}

func applyDeterministicFloor(decision domain.Decision, floor *domain.Decision) domain.Decision {
	if floor == nil {
		return decision
	}
	floorWon := false
	if severityRank(floor.Severity) > severityRank(decision.Severity) {
		decision.Severity = floor.Severity
		floorWon = true
	}
	if actionRank(floor.Action) > actionRank(decision.Action) {
		decision.Action = floor.Action
		floorWon = true
	}
	decision.RuleIDs = mergeRuleIDs(decision.RuleIDs, floor.RuleIDs)
	decision.ReasonCodes = mergeStrings(decision.ReasonCodes, floor.ReasonCodes)
	if floorWon && floor.Summary != "" {
		decision.Summary = floor.Summary
	}
	return decision
}

func severityRank(severity domain.Severity) int {
	switch severity {
	case domain.SeverityCritical:
		return 3
	case domain.SeverityWarning:
		return 2
	case domain.SeverityInfo:
		return 1
	default:
		return 0
	}
}

func actionRank(action domain.Action) int {
	switch action {
	case domain.ActionPage:
		return 4
	case domain.ActionNotify:
		return 3
	case domain.ActionRecord:
		return 2
	case domain.ActionSuppress:
		return 1
	default:
		return 0
	}
}

func ruleIDs(result rules.Result) []string {
	ids := make([]string, 0, len(result.Matches))
	for _, match := range result.Matches {
		ids = append(ids, match.RuleID)
	}
	return ids
}

func mergeRuleIDs(left, right []string) []string {
	return mergeStrings(left, right)
}

func mergeStrings(left, right []string) []string {
	seen := make(map[string]struct{}, len(left)+len(right))
	for _, value := range left {
		if value != "" {
			seen[value] = struct{}{}
		}
	}
	for _, value := range right {
		if value != "" {
			seen[value] = struct{}{}
		}
	}
	merged := make([]string, 0, len(seen))
	for value := range seen {
		merged = append(merged, value)
	}
	sort.Strings(merged)
	return merged
}

func cloneDecision(source domain.Decision) domain.Decision {
	source.ReasonCodes = append([]string(nil), source.ReasonCodes...)
	source.RuleIDs = append([]string(nil), source.RuleIDs...)
	return source
}
