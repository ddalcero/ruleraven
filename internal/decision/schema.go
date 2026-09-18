package decision

import "github.com/ddalcero/ruleraven/internal/provider"

// AnswerJSONSchema exposes the strict provider answer contract beside the
// versioned question sets that define it.
func AnswerJSONSchema(questions map[string]provider.Question) ([]byte, error) {
	return provider.AnswerJSONSchema(questions)
}
