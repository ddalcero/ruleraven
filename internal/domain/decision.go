package domain

type Severity string
type Action string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"

	ActionRecord   Action = "record"
	ActionNotify   Action = "notify"
	ActionPage     Action = "page"
	ActionSuppress Action = "suppress"
)

type Decision struct {
	Severity    Severity `json:"severity"`
	Action      Action   `json:"action"`
	Summary     string   `json:"summary"`
	ReasonCodes []string `json:"reasonCodes,omitempty"`
	RuleIDs     []string `json:"ruleIds,omitempty"`
}
