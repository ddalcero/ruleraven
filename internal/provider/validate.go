package provider

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

const probabilityTolerance = 1e-9

func ValidateQuestions(questions map[string]Question) error {
	if len(questions) == 0 {
		return fmt.Errorf("questions must not be empty")
	}
	for _, id := range sortedQuestionIDs(questions) {
		question := questions[id]
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("question ID must not be empty")
		}
		if strings.TrimSpace(question.Instructions) == "" {
			return fmt.Errorf("question %q instructions must not be empty", id)
		}
		switch question.Type {
		case QuestionTypeNoul:
			if len(question.Levels) != 0 {
				return fmt.Errorf("question %q: noul must not define score levels", id)
			}
			if len(question.Criteria) != 0 {
				if len(question.Criteria) != 2 {
					return fmt.Errorf("question %q: noul criteria must define true and false", id)
				}
				if _, ok := question.Criteria["true"]; !ok {
					return fmt.Errorf("question %q: noul criteria missing true", id)
				}
				if _, ok := question.Criteria["false"]; !ok {
					return fmt.Errorf("question %q: noul criteria missing false", id)
				}
			}
		case QuestionTypeChoice:
			if len(question.Levels) != 0 {
				return fmt.Errorf("question %q: choice must not define score levels", id)
			}
			if len(question.Criteria) < 2 {
				return fmt.Errorf("question %q: choice must define at least two criteria", id)
			}
			for choice := range question.Criteria {
				if strings.TrimSpace(choice) == "" {
					return fmt.Errorf("question %q: choice ID must not be empty", id)
				}
			}
		case QuestionTypeScore:
			if len(question.Criteria) != 0 {
				return fmt.Errorf("question %q: score must not define choice criteria", id)
			}
			if len(question.Levels) < 2 {
				return fmt.Errorf("question %q: score must define at least two levels", id)
			}
			seen := make(map[string]struct{}, len(question.Levels))
			for _, level := range question.Levels {
				if strings.TrimSpace(level) == "" {
					return fmt.Errorf("question %q: score level must not be empty", id)
				}
				if _, duplicate := seen[level]; duplicate {
					return fmt.Errorf("question %q: duplicate score level %q", id, level)
				}
				seen[level] = struct{}{}
			}
		default:
			return fmt.Errorf("question %q: unknown type %q", id, question.Type)
		}
	}
	return nil
}

func ValidateAnswers(questions map[string]Question, answers map[string]Answer) error {
	if err := ValidateQuestions(questions); err != nil {
		return fmt.Errorf("invalid questions: %w", err)
	}
	for _, id := range sortedAnswerIDs(answers) {
		if _, ok := questions[id]; !ok {
			return fmt.Errorf("unknown answer %q", id)
		}
	}
	for _, id := range sortedQuestionIDs(questions) {
		question := questions[id]
		answer, ok := answers[id]
		if !ok {
			return fmt.Errorf("missing answer %q", id)
		}
		if answer.Type != question.Type {
			return fmt.Errorf("answer %q type %q does not match question type %q", id, answer.Type, question.Type)
		}
		var err error
		switch question.Type {
		case QuestionTypeNoul:
			err = validateNoulAnswer(answer)
		case QuestionTypeChoice:
			err = validateChoiceAnswer(question, answer)
		case QuestionTypeScore:
			err = validateScoreAnswer(question, answer)
		}
		if err != nil {
			return fmt.Errorf("answer %q: %w", id, err)
		}
	}
	return nil
}

func validateNoulAnswer(answer Answer) error {
	if answer.Noul == nil {
		return fmt.Errorf("noul value is required")
	}
	if err := validateUnitValue("noul", *answer.Noul); err != nil {
		return err
	}
	if answer.Choice != "" {
		return fmt.Errorf("choice is not allowed on a noul answer")
	}
	if answer.Score != nil {
		return fmt.Errorf("score is not allowed on a noul answer")
	}
	if len(answer.Legend) != 0 {
		return fmt.Errorf("legend is not allowed on a noul answer")
	}
	if len(answer.Probabilities) != 0 {
		return fmt.Errorf("probabilities are not allowed on a noul answer")
	}
	if answer.Confidence != nil {
		return fmt.Errorf("confidence is not allowed on a noul answer")
	}
	return nil
}

func validateChoiceAnswer(question Question, answer Answer) error {
	if answer.Noul != nil || answer.Score != nil || len(answer.Legend) != 0 {
		return fmt.Errorf("fields for another answer type are not allowed on a choice answer")
	}
	if _, ok := question.Criteria[answer.Choice]; !ok {
		return fmt.Errorf("unknown choice %q", answer.Choice)
	}
	if err := validateConfidence(answer.Confidence); err != nil {
		return err
	}
	keys := make([]string, 0, len(question.Criteria))
	for key := range question.Criteria {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if err := validateDistribution(keys, answer.Probabilities); err != nil {
		return err
	}
	selected := answer.Probabilities[answer.Choice]
	for _, probability := range answer.Probabilities {
		if probability > selected+probabilityTolerance {
			return fmt.Errorf("choice %q does not have the highest probability", answer.Choice)
		}
	}
	return nil
}

func validateScoreAnswer(question Question, answer Answer) error {
	if answer.Noul != nil || answer.Choice != "" {
		return fmt.Errorf("fields for another answer type are not allowed on a score answer")
	}
	if answer.Score == nil {
		return fmt.Errorf("score is required")
	}
	if !isFinite(*answer.Score) {
		return fmt.Errorf("score must be finite")
	}
	if *answer.Score < 0 || *answer.Score > float64(len(question.Levels)-1) {
		return fmt.Errorf("score must be between 0 and %d", len(question.Levels)-1)
	}
	if err := validateConfidence(answer.Confidence); err != nil {
		return err
	}
	if len(answer.Legend) != len(question.Levels) {
		return fmt.Errorf("legend must contain exactly %d levels", len(question.Levels))
	}
	seenValues := make(map[string]struct{}, len(answer.Legend))
	keys := make([]string, len(question.Levels))
	for index, expected := range question.Levels {
		key := strconv.Itoa(index)
		keys[index] = key
		actual, ok := answer.Legend[key]
		if !ok {
			return fmt.Errorf("legend is missing level %q", key)
		}
		if _, duplicate := seenValues[actual]; duplicate {
			return fmt.Errorf("legend contains duplicate value %q", actual)
		}
		seenValues[actual] = struct{}{}
		if actual != expected {
			return fmt.Errorf("legend level %q is %q, want %q", key, actual, expected)
		}
	}
	if err := validateDistribution(keys, answer.Probabilities); err != nil {
		return err
	}
	weighted := 0.0
	for index, key := range keys {
		weighted += float64(index) * answer.Probabilities[key]
	}
	if math.Abs(weighted-*answer.Score) > 1e-6 {
		return fmt.Errorf("score %g does not match probability-weighted value %g", *answer.Score, weighted)
	}
	return nil
}

func validateConfidence(confidence *float64) error {
	if confidence == nil {
		return fmt.Errorf("confidence is required")
	}
	return validateUnitValue("confidence", *confidence)
}

func validateDistribution(expectedKeys []string, probabilities map[string]float64) error {
	if len(probabilities) != len(expectedKeys) {
		return fmt.Errorf("probabilities must contain exactly %d values", len(expectedKeys))
	}
	expected := make(map[string]struct{}, len(expectedKeys))
	for _, key := range expectedKeys {
		expected[key] = struct{}{}
	}
	sum := 0.0
	for key, probability := range probabilities {
		if _, ok := expected[key]; !ok {
			return fmt.Errorf("probabilities contain unknown key %q", key)
		}
		if !isFinite(probability) {
			return fmt.Errorf("probability %q must be finite", key)
		}
		if probability < 0 || probability > 1 {
			return fmt.Errorf("probability %q must be between 0 and 1", key)
		}
		sum += probability
	}
	if math.Abs(sum-1) > probabilityTolerance {
		return fmt.Errorf("probabilities must sum to 1, got %g", sum)
	}
	return nil
}

func validateUnitValue(name string, value float64) error {
	if !isFinite(value) {
		return fmt.Errorf("%s must be finite", name)
	}
	if value < 0 || value > 1 {
		return fmt.Errorf("%s must be between 0 and 1", name)
	}
	return nil
}

func isFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func sortedQuestionIDs(questions map[string]Question) []string {
	ids := make([]string, 0, len(questions))
	for id := range questions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func sortedAnswerIDs(answers map[string]Answer) []string {
	ids := make([]string, 0, len(answers))
	for id := range answers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
