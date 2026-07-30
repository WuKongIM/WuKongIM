package issueagent

import (
	"errors"
	"io"
	"slices"
)

// EngineerOutcome is Codex's advisory conclusion for one ephemeral task.
type EngineerOutcome string

const (
	EngineerOutcomeReady        EngineerOutcome = "ready"
	EngineerOutcomeNeedsHuman   EngineerOutcome = "needs_human"
	EngineerOutcomeAlreadyFixed EngineerOutcome = "already_fixed"
	EngineerOutcomeFailed       EngineerOutcome = "failed"
)

// EngineerResult is untrusted model-authored analysis, never test evidence.
type EngineerResult struct {
	SchemaVersion         int             `json:"schema_version"`
	Repository            string          `json:"repository"`
	IssueNumber           int64           `json:"issue_number"`
	TaskID                string          `json:"task_id"`
	Outcome               EngineerOutcome `json:"outcome"`
	ExternalSymptom       string          `json:"external_symptom"`
	RootCause             string          `json:"root_cause"`
	CausalPath            string          `json:"causal_path"`
	EvidenceReferences    []string        `json:"evidence_references"`
	ProposedRisk          []string        `json:"proposed_risk"`
	TestsAttempted        []string        `json:"tests_attempted"`
	UnresolvedUncertainty string          `json:"unresolved_uncertainty"`
	Summary               string          `json:"summary"`
	Ready                 bool            `json:"ready"`
}

// DecodeEngineerResult decodes one bounded advisory Codex result.
func DecodeEngineerResult(reader io.Reader, maxBytes int64) (EngineerResult, error) {
	var result EngineerResult
	if err := decodeStrictJSON(reader, maxBytes, &result); err != nil {
		return EngineerResult{}, err
	}
	if err := ValidateEngineerResult(result); err != nil {
		return EngineerResult{}, err
	}
	return result, nil
}

// ValidateEngineerResult rejects ambiguous or unbounded advisory output.
func ValidateEngineerResult(result EngineerResult) error {
	if result.SchemaVersion != 2 || !validRepository(result.Repository) ||
		result.IssueNumber <= 0 || !digestPattern.MatchString(result.TaskID) {
		return errors.New("invalid Engineer result identity")
	}
	switch result.Outcome {
	case EngineerOutcomeReady,
		EngineerOutcomeNeedsHuman,
		EngineerOutcomeAlreadyFixed,
		EngineerOutcomeFailed:
	default:
		return errors.New("invalid Engineer outcome")
	}
	for _, value := range []struct {
		text     string
		maxBytes int
		required bool
	}{
		{result.ExternalSymptom, 4096, true},
		{result.RootCause, 8192, result.Outcome == EngineerOutcomeReady},
		{result.CausalPath, 8192, result.Outcome == EngineerOutcomeReady},
		{result.UnresolvedUncertainty, 4096, false},
		{result.Summary, 4096, true},
	} {
		if value.text == "" && !value.required {
			continue
		}
		if !validContextText(value.text, value.maxBytes, true) {
			return errors.New("invalid Engineer result text")
		}
	}
	if !boundedResultStrings(result.EvidenceReferences, 512, false) ||
		!boundedResultStrings(result.ProposedRisk, 256, true) ||
		!boundedResultStrings(result.TestsAttempted, 1024, false) {
		return errors.New("invalid Engineer result lists")
	}
	if result.Outcome == EngineerOutcomeReady != result.Ready {
		return errors.New("Engineer readiness contradicts outcome")
	}
	if result.Outcome == EngineerOutcomeReady &&
		(len(result.EvidenceReferences) == 0 ||
			len(result.TestsAttempted) == 0) {
		return errors.New("ready Engineer result lacks diagnosis or test references")
	}
	return nil
}

func boundedResultStrings(values []string, maxBytes int, sorted bool) bool {
	if len(values) > maxContextItems || sorted && !slices.IsSorted(values) {
		return false
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !validContextText(value, maxBytes, true) {
			return false
		}
		if _, exists := seen[value]; exists {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}
