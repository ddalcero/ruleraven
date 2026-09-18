package rules

import (
	"sort"
	"strings"
	"time"

	"github.com/ddalcero/ruleraven/internal/domain"
)

const PolicyVersion = "deterministic-triage-v1"

type Disposition string

const (
	DispositionTerminal Disposition = "terminal"
	DispositionSemantic Disposition = "semantic"
	DispositionSuppress Disposition = "suppress"
	DispositionNoMatch  Disposition = "no_match"
)

type Match struct {
	RuleID   string          `json:"ruleId"`
	Reason   string          `json:"reason"`
	Severity domain.Severity `json:"severity,omitempty"`
}

type Result struct {
	Matches       []Match          `json:"matches,omitempty"`
	Disposition   Disposition      `json:"disposition"`
	Decision      *domain.Decision `json:"decision,omitempty"`
	QuestionSet   string           `json:"questionSet,omitempty"`
	PolicyVersion string           `json:"policyVersion"`
}

type Options struct {
	Now                        func() time.Time
	CrashLoopRestartThreshold  int
	CrashLoopCriticalThreshold int
	MinimumAge                 time.Duration
	EventQuietPeriod           time.Duration
	IgnoredNamespaces          []string
	IgnoredLabels              []string
}

type Engine struct {
	now     func() time.Time
	options Options
}

func NewEngine(options Options) *Engine {
	if options.Now == nil {
		options.Now = time.Now
	}
	options.IgnoredNamespaces = append([]string(nil), options.IgnoredNamespaces...)
	options.IgnoredLabels = append([]string(nil), options.IgnoredLabels...)
	sort.Strings(options.IgnoredNamespaces)
	sort.Strings(options.IgnoredLabels)
	return &Engine{now: options.Now, options: options}
}

func (e *Engine) Evaluate(snapshot domain.Snapshot, previous *domain.Incident) Result {
	if reason, ignored := e.ignoredReason(snapshot); ignored {
		match := Match{RuleID: "ignored", Reason: reason, Severity: domain.SeverityInfo}
		decision := domain.Decision{
			Severity: domain.SeverityInfo, Action: domain.ActionSuppress, Summary: reason,
			ReasonCodes: []string{"ignored"}, RuleIDs: []string{"ignored"},
		}
		return Result{
			Matches: []Match{match}, Disposition: DispositionSuppress,
			Decision: &decision, PolicyVersion: PolicyVersion,
		}
	}
	matches := e.podCrashLoop(snapshot)
	matches = append(matches, e.podUnschedulable(snapshot)...)
	matches = append(matches, e.imagePullFailure(snapshot)...)
	matches = append(matches, e.failedJob(snapshot)...)
	matches = append(matches, e.workloadUnavailable(snapshot)...)
	matches = append(matches, e.warningEvent(snapshot)...)
	sort.Slice(matches, func(i, j int) bool { return matches[i].RuleID < matches[j].RuleID })
	if len(matches) == 0 {
		if recovered := e.recovered(previous); recovered != nil {
			return *recovered
		}
		return Result{Disposition: DispositionNoMatch, PolicyVersion: PolicyVersion}
	}
	if len(matches) == 1 && matches[0].RuleID == "warning-event" {
		return Result{
			Matches: matches, Disposition: DispositionSemantic,
			QuestionSet: "operational-triage-v1", PolicyVersion: PolicyVersion,
		}
	}
	decision := decisionFor(matches)
	return Result{Matches: matches, Disposition: DispositionTerminal, Decision: &decision, PolicyVersion: PolicyVersion}
}

func (e *Engine) ignoredReason(snapshot domain.Snapshot) (string, bool) {
	for _, namespace := range e.options.IgnoredNamespaces {
		if snapshot.Source.Namespace == namespace {
			return "ignored namespace " + namespace, true
		}
	}
	for _, selector := range e.options.IgnoredLabels {
		key, value, hasValue := strings.Cut(selector, "=")
		actual, exists := snapshot.Labels[key]
		if exists && (!hasValue || actual == value) {
			return "ignored label " + selector, true
		}
	}
	return "", false
}

func highestSeverity(matches []Match) Match {
	winning := matches[0]
	for _, match := range matches[1:] {
		if severityRank(match.Severity) > severityRank(winning.Severity) {
			winning = match
		}
	}
	return winning
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

func decisionFor(matches []Match) domain.Decision {
	winning := highestSeverity(matches)
	action := domain.ActionNotify
	if winning.Severity == domain.SeverityCritical {
		action = domain.ActionPage
	}
	ruleIDs := make([]string, len(matches))
	for i, match := range matches {
		ruleIDs[i] = match.RuleID
	}
	return domain.Decision{
		Severity: winning.Severity, Action: action, Summary: winning.Reason,
		ReasonCodes: append([]string(nil), ruleIDs...), RuleIDs: ruleIDs,
	}
}
