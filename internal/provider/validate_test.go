package provider

import (
	"context"
	"math"
	"strings"
	"testing"
)

func validationQuestions() map[string]Question {
	return map[string]Question{
		"urgent": {
			Type: QuestionTypeNoul, Instructions: "Does this require immediate attention?",
			Criteria: map[string]string{"true": "Immediate action is needed", "false": "Action can wait"},
		},
		"family": {
			Type: QuestionTypeChoice, Instructions: "Which incident family fits best?",
			Criteria: map[string]string{"application": "Application failure", "infrastructure": "Infrastructure failure"},
		},
		"impact": {
			Type: QuestionTypeScore, Instructions: "How broad is the operational impact?",
			Levels: []string{"No user impact", "Degraded", "Unavailable"},
		},
	}
}

func validAnswers() map[string]Answer {
	urgent := 0.9
	score := 1.5
	confidence := 0.8
	return map[string]Answer{
		"urgent": {Type: QuestionTypeNoul, Noul: &urgent},
		"family": {
			Type: QuestionTypeChoice, Choice: "application",
			Probabilities: map[string]float64{"application": 0.8, "infrastructure": 0.2}, Confidence: &confidence,
		},
		"impact": {
			Type: QuestionTypeScore, Score: &score,
			Legend:        map[string]string{"0": "No user impact", "1": "Degraded", "2": "Unavailable"},
			Probabilities: map[string]float64{"0": 0.1, "1": 0.3, "2": 0.6}, Confidence: &confidence,
		},
	}
}

func cloneAnswers(src map[string]Answer) map[string]Answer {
	dst := make(map[string]Answer, len(src))
	for id, answer := range src {
		answer.Legend = cloneStrings(answer.Legend)
		answer.Probabilities = cloneFloats(answer.Probabilities)
		dst[id] = answer
	}
	return dst
}

func cloneStrings(src map[string]string) map[string]string {
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func cloneFloats(src map[string]float64) map[string]float64 {
	dst := make(map[string]float64, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func TestValidateAnswers(t *testing.T) {
	questions := validationQuestions()
	if err := ValidateAnswers(questions, validAnswers()); err != nil {
		t.Fatalf("valid answers rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(map[string]Answer)
		want   string
	}{
		{name: "missing id", mutate: func(a map[string]Answer) { delete(a, "urgent") }, want: "missing answer"},
		{name: "extra id", mutate: func(a map[string]Answer) { a["extra"] = Answer{Type: QuestionTypeNoul} }, want: "unknown answer"},
		{name: "wrong type", mutate: func(a map[string]Answer) { x := a["urgent"]; x.Type = QuestionTypeScore; a["urgent"] = x }, want: "type"},
		{name: "unknown choice", mutate: func(a map[string]Answer) { x := a["family"]; x.Choice = "other"; a["family"] = x }, want: "unknown choice"},
		{name: "probability below zero", mutate: func(a map[string]Answer) { x := a["family"]; x.Probabilities["application"] = -0.1; a["family"] = x }, want: "probability"},
		{name: "probability above one", mutate: func(a map[string]Answer) { x := a["family"]; x.Probabilities["application"] = 1.1; a["family"] = x }, want: "probability"},
		{name: "probability is NaN", mutate: func(a map[string]Answer) {
			x := a["family"]
			x.Probabilities["application"] = math.NaN()
			a["family"] = x
		}, want: "finite"},
		{name: "score is infinite", mutate: func(a map[string]Answer) { x := a["impact"]; value := math.Inf(1); x.Score = &value; a["impact"] = x }, want: "finite"},
		{name: "duplicate legend values", mutate: func(a map[string]Answer) { x := a["impact"]; x.Legend["2"] = "Degraded"; a["impact"] = x }, want: "legend"},
		{name: "confidence on noul", mutate: func(a map[string]Answer) { x := a["urgent"]; value := 0.5; x.Confidence = &value; a["urgent"] = x }, want: "confidence"},
		{name: "partial choice distribution", mutate: func(a map[string]Answer) {
			x := a["family"]
			delete(x.Probabilities, "infrastructure")
			a["family"] = x
		}, want: "probabilities"},
		{name: "distribution does not sum to one", mutate: func(a map[string]Answer) { x := a["family"]; x.Probabilities["application"] = 0.7; a["family"] = x }, want: "sum"},
		{name: "unexpected noul field", mutate: func(a map[string]Answer) { x := a["urgent"]; x.Choice = "yes"; a["urgent"] = x }, want: "choice"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			answers := cloneAnswers(validAnswers())
			test.mutate(answers)
			err := ValidateAnswers(questions, answers)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateAnswers error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestValidateAnswersRejectsInvalidQuestionDefinitions(t *testing.T) {
	questions := validationQuestions()
	questions["impact"] = Question{Type: QuestionTypeScore, Instructions: "impact", Levels: []string{"same", "same"}}
	if err := ValidateAnswers(questions, validAnswers()); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("error = %v, want duplicate-level rejection", err)
	}
}

var _ Provider = (*stubProvider)(nil)

type stubProvider struct{}

func (*stubProvider) Name() string { return "stub" }
func (*stubProvider) Evaluate(context.Context, EvaluationRequest) (EvaluationResponse, error) {
	return EvaluationResponse{}, nil
}
