package decision

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/ddalcero/ruleraven/internal/domain"
	"github.com/ddalcero/ruleraven/internal/provider"
	"github.com/ddalcero/ruleraven/internal/rules"
)

func TestOperationalTriageV1Questions(t *testing.T) {
	questions := OperationalTriageV1Questions()
	want := map[string]provider.QuestionType{
		QuestionIncidentFamily:             provider.QuestionTypeChoice,
		QuestionOperationalImpact:          provider.QuestionTypeScore,
		QuestionRequiresImmediateAttention: provider.QuestionTypeNoul,
		QuestionLikelyTransient:            provider.QuestionTypeNoul,
	}
	if len(questions) != len(want) {
		t.Fatalf("question count = %d, want %d", len(questions), len(want))
	}
	for id, questionType := range want {
		question, ok := questions[id]
		if !ok || question.Type != questionType || question.Instructions == "" {
			t.Fatalf("question %q = %#v, want type %q with instructions", id, question, questionType)
		}
	}
	if err := provider.ValidateQuestions(questions); err != nil {
		t.Fatalf("question set is invalid: %v", err)
	}
}

func TestOperationalTriageAnswerSchemaGolden(t *testing.T) {
	got, err := AnswerJSONSchema(OperationalTriageV1Questions())
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join("testdata", "answer-schema.golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("answer schema changed; update intentionally if the contract changed\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestComposerUsesVersionedThresholds(t *testing.T) {
	composer := NewComposer()
	if composer.PolicyVersion() != CompositionPolicyVersion {
		t.Fatalf("policy version = %q, want %q", composer.PolicyVersion(), CompositionPolicyVersion)
	}

	tests := []struct {
		name      string
		impact    float64
		immediate float64
		transient float64
		severity  domain.Severity
		action    domain.Action
	}{
		{name: "low impact", impact: 0.5, immediate: 0.2, transient: 0.8, severity: domain.SeverityInfo, action: domain.ActionRecord},
		{name: "warning impact threshold", impact: 1.0, immediate: 0.2, transient: 0.8, severity: domain.SeverityWarning, action: domain.ActionNotify},
		{name: "critical impact threshold", impact: 2.5, immediate: 0.2, transient: 0.2, severity: domain.SeverityCritical, action: domain.ActionPage},
		{name: "immediate attention threshold", impact: 1.2, immediate: 0.85, transient: 0.2, severity: domain.SeverityCritical, action: domain.ActionPage},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := semanticRuleResult(nil)
			decision := composer.Compose(result, operationalAnswers(test.impact, test.immediate, test.transient), nil)
			if decision.Severity != test.severity || decision.Action != test.action {
				t.Fatalf("decision = %#v, want %s/%s", decision, test.severity, test.action)
			}
		})
	}
}

func TestComposerFallsBackWhenProviderUnavailableOrMalformed(t *testing.T) {
	composer := NewComposer()
	result := semanticRuleResult(nil)
	for _, test := range []struct {
		name    string
		answers map[string]provider.Answer
		err     error
	}{
		{name: "provider unavailable", err: errProviderUnavailable{}},
		{name: "malformed output", answers: map[string]provider.Answer{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := composer.Compose(result, test.answers, test.err)
			if got.Severity != domain.SeverityWarning || got.Action != domain.ActionNotify || !reflect.DeepEqual(got.ReasonCodes, []string{"provider_unavailable"}) {
				t.Fatalf("fallback = %#v, want warning/notify provider_unavailable", got)
			}
		})
	}
}

func TestComposerCannotDowngradeDeterministicCriticalFloor(t *testing.T) {
	floor := &domain.Decision{
		Severity: domain.SeverityCritical, Action: domain.ActionPage,
		Summary: "deterministic outage", ReasonCodes: []string{"pod-crash-loop"}, RuleIDs: []string{"pod-crash-loop"},
	}
	result := semanticRuleResult(floor)
	got := NewComposer().Compose(result, operationalAnswers(0.1, 0.1, 0.9), nil)
	if got.Severity != domain.SeverityCritical || got.Action != domain.ActionPage {
		t.Fatalf("decision = %#v, deterministic critical floor was downgraded", got)
	}
	if !slices.Contains(got.RuleIDs, "pod-crash-loop") {
		t.Fatalf("rule IDs = %v, want deterministic floor evidence retained", got.RuleIDs)
	}
}

func semanticRuleResult(floor *domain.Decision) rules.Result {
	return rules.Result{
		Disposition: rules.DispositionSemantic, Decision: floor,
		Matches:     []rules.Match{{RuleID: "warning-event", Severity: domain.SeverityWarning, Reason: "semantic triage"}},
		QuestionSet: OperationalTriageV1, PolicyVersion: rules.PolicyVersion,
	}
}

func operationalAnswers(impact, immediate, transient float64) map[string]provider.Answer {
	confidence := 0.8
	return map[string]provider.Answer{
		QuestionIncidentFamily: {
			Type: provider.QuestionTypeChoice, Choice: "infrastructure",
			Probabilities: map[string]float64{
				"application": 0.05, "configuration": 0.05, "dependency": 0.1,
				"infrastructure": 0.7, "security": 0.05, "unknown": 0.05,
			}, Confidence: &confidence,
		},
		QuestionOperationalImpact: {
			Type: provider.QuestionTypeScore, Score: &impact,
			Legend: map[string]string{
				"0": "No current user-visible impact", "1": "Limited degradation or a working workaround",
				"2": "Material degradation affecting multiple users or a critical workload", "3": "Broad outage, data-loss risk, or security impact",
			},
			Probabilities: scoreProbabilities(impact), Confidence: &confidence,
		},
		QuestionRequiresImmediateAttention: {Type: provider.QuestionTypeNoul, Noul: &immediate},
		QuestionLikelyTransient:            {Type: provider.QuestionTypeNoul, Noul: &transient},
	}
}

func scoreProbabilities(score float64) map[string]float64 {
	// Composer behavior is the subject of these tests; use a valid distribution
	// whose weighted score matches the boundary value under test.
	probabilities := map[string]float64{"0": 0, "1": 0, "2": 0, "3": 0}
	lower := int(score)
	if lower >= 3 {
		probabilities["3"] = 1
		return probabilities
	}
	fraction := score - float64(lower)
	probabilities[string(rune('0'+lower))] = 1 - fraction
	probabilities[string(rune('0'+lower+1))] = fraction
	return probabilities
}

type errProviderUnavailable struct{}

func (errProviderUnavailable) Error() string { return "provider unavailable" }
