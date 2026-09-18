package decision

import "github.com/ddalcero/ruleraven/internal/provider"

const (
	OperationalTriageV1 = "operational-triage-v1"

	QuestionIncidentFamily             = "incident_family"
	QuestionOperationalImpact          = "operational_impact"
	QuestionRequiresImmediateAttention = "requires_immediate_attention"
	QuestionLikelyTransient            = "likely_transient"
)

var operationalTriageV1 = map[string]provider.Question{
	QuestionIncidentFamily: {
		Type:         provider.QuestionTypeChoice,
		Instructions: "Which incident family best explains the observed operational state?",
		Criteria: map[string]string{
			"application":    "Application process, runtime, or workload behavior",
			"configuration":  "Invalid or incompatible workload or cluster configuration",
			"dependency":     "A required internal or external dependency is failing",
			"infrastructure": "Cluster, node, storage, or networking infrastructure",
			"security":       "A suspected security or access-control issue",
			"unknown":        "The bounded evidence does not support another family",
		},
	},
	QuestionOperationalImpact: {
		Type:         provider.QuestionTypeScore,
		Instructions: "Rate the current operational impact from the bounded evidence.",
		Levels: []string{
			"No current user-visible impact",
			"Limited degradation or a working workaround",
			"Material degradation affecting multiple users or a critical workload",
			"Broad outage, data-loss risk, or security impact",
		},
	},
	QuestionRequiresImmediateAttention: {
		Type:         provider.QuestionTypeNoul,
		Instructions: "Does the incident require immediate human attention?",
		Criteria: map[string]string{
			"true":  "Delay is likely to materially increase operational harm",
			"false": "The incident can safely wait for normal triage",
		},
	},
	QuestionLikelyTransient: {
		Type:         provider.QuestionTypeNoul,
		Instructions: "Is the observed condition likely to clear without intervention?",
		Criteria: map[string]string{
			"true":  "The evidence indicates a short-lived or self-healing condition",
			"false": "The condition is likely persistent or requires intervention",
		},
	},
}

// OperationalTriageV1Questions returns a defensive copy of the versioned
// provider-neutral rubric.
func OperationalTriageV1Questions() map[string]provider.Question {
	questions := make(map[string]provider.Question, len(operationalTriageV1))
	for id, question := range operationalTriageV1 {
		question.Criteria = cloneStringMap(question.Criteria)
		question.Levels = append([]string(nil), question.Levels...)
		questions[id] = question
	}
	return questions
}

func cloneStringMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	cloned := make(map[string]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}
