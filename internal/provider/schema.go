package provider

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
)

// AnswerJSONSchema returns a strict JSON Schema for a root object containing
// exactly one typed answer for every supplied question.
func AnswerJSONSchema(questions map[string]Question) ([]byte, error) {
	if err := ValidateQuestions(questions); err != nil {
		return nil, err
	}
	ids := sortedQuestionIDs(questions)
	answerProperties := make(map[string]any, len(ids))
	for _, id := range ids {
		answerProperties[id] = answerSchemaFor(questions[id])
	}
	schema := map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"title":                "RuleRaven provider answers",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"answers"},
		"properties": map[string]any{
			"answers": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required":             ids,
				"properties":           answerProperties,
			},
		},
	}
	encoded, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal answer schema: %w", err)
	}
	return append(encoded, '\n'), nil
}

func answerSchemaFor(question Question) map[string]any {
	switch question.Type {
	case QuestionTypeNoul:
		return strictObject(
			[]string{"type", "noul"},
			map[string]any{
				"type": constString(string(QuestionTypeNoul)),
				"noul": unitNumber(),
			},
		)
	case QuestionTypeChoice:
		choices := make([]string, 0, len(question.Criteria))
		for choice := range question.Criteria {
			choices = append(choices, choice)
		}
		sort.Strings(choices)
		return strictObject(
			[]string{"type", "choice", "probabilities", "confidence"},
			map[string]any{
				"type":          constString(string(QuestionTypeChoice)),
				"choice":        map[string]any{"type": "string", "enum": choices},
				"probabilities": probabilityObject(choices),
				"confidence":    unitNumber(),
			},
		)
	case QuestionTypeScore:
		keys := make([]string, len(question.Levels))
		legendProperties := make(map[string]any, len(question.Levels))
		for index, level := range question.Levels {
			key := strconv.Itoa(index)
			keys[index] = key
			legendProperties[key] = constString(level)
		}
		return strictObject(
			[]string{"type", "score", "legend", "probabilities", "confidence"},
			map[string]any{
				"type":          constString(string(QuestionTypeScore)),
				"score":         map[string]any{"type": "number", "minimum": 0, "maximum": len(question.Levels) - 1},
				"legend":        strictObject(keys, legendProperties),
				"probabilities": probabilityObject(keys),
				"confidence":    unitNumber(),
			},
		)
	default:
		panic("question validation allowed an unknown type")
	}
}

func strictObject(required []string, properties map[string]any) map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             required,
		"properties":           properties,
	}
}

func probabilityObject(keys []string) map[string]any {
	properties := make(map[string]any, len(keys))
	for _, key := range keys {
		properties[key] = unitNumber()
	}
	return strictObject(keys, properties)
}

func unitNumber() map[string]any {
	return map[string]any{"type": "number", "minimum": 0, "maximum": 1}
}

func constString(value string) map[string]any {
	return map[string]any{"type": "string", "const": value}
}
