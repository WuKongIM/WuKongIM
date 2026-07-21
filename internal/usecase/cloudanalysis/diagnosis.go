package cloudanalysis

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const maxDiagnosisBytes = 256 << 10

var (
	// ErrInvalidDiagnosis reports a Diagnosis Result outside the repository contract.
	ErrInvalidDiagnosis = errors.New("internal/usecase/cloudanalysis: invalid diagnosis")
	diagnosisSHA        = regexp.MustCompile(`^[0-9a-f]{40}$`)
	diagnosisDigest     = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// DiagnosisVerdict classifies the highest-confidence run conclusion.
type DiagnosisVerdict string

const (
	// VerdictHealthy means the completed workload passed and no defect signal remains.
	VerdictHealthy DiagnosisVerdict = "healthy"
	// VerdictProductDefect means evidence attributes the failure to repository code.
	VerdictProductDefect DiagnosisVerdict = "product_defect"
	// VerdictInfrastructureInterrupted means cloud infrastructure invalidated the run.
	VerdictInfrastructureInterrupted DiagnosisVerdict = "infrastructure_interrupted"
	// VerdictScenarioInvalid means the declared workload was not applied as intended.
	VerdictScenarioInvalid DiagnosisVerdict = "scenario_invalid"
	// VerdictInsufficientEvidence means the live evidence cannot support attribution.
	VerdictInsufficientEvidence DiagnosisVerdict = "insufficient_evidence"
)

// DiagnosisSeverity is the bounded operational impact classification.
type DiagnosisSeverity string

const (
	// SeverityNone means no defect impact is present.
	SeverityNone DiagnosisSeverity = "none"
	// SeverityLow means limited, non-urgent impact.
	SeverityLow DiagnosisSeverity = "low"
	// SeverityMedium means material but bounded impact.
	SeverityMedium DiagnosisSeverity = "medium"
	// SeverityHigh means substantial run or product impact.
	SeverityHigh DiagnosisSeverity = "high"
	// SeverityCritical means severe correctness or availability impact.
	SeverityCritical DiagnosisSeverity = "critical"
)

// RootCauseScope is the bounded ownership classification for a diagnosis.
type RootCauseScope string

const (
	// RootCauseNone means a healthy result has no defect owner.
	RootCauseNone RootCauseScope = "none"
	// RootCauseProduct assigns the cause to WuKongIM repository behavior.
	RootCauseProduct RootCauseScope = "product"
	// RootCauseInfrastructure assigns the cause to cloud or host behavior.
	RootCauseInfrastructure RootCauseScope = "infrastructure"
	// RootCauseScenario assigns the cause to the workload contract or generator.
	RootCauseScenario RootCauseScope = "scenario"
	// RootCauseUnknown means evidence cannot assign ownership.
	RootCauseUnknown RootCauseScope = "unknown"
)

// DiagnosisResult is the compact, non-sensitive handoff from live diagnosis to remediation.
type DiagnosisResult struct {
	// Schema is the exact versioned Diagnosis Result contract.
	Schema string `json:"schema"`
	// RunIdentity binds the result to immutable deployed inputs.
	RunIdentity DiagnosisRunIdentity `json:"run_identity"`
	// AnalyzedWindow is the exact live interval considered.
	AnalyzedWindow DiagnosisWindow `json:"analyzed_window"`
	// Verdict is the bounded conclusion classification.
	Verdict DiagnosisVerdict `json:"verdict"`
	// Severity is the bounded operational impact.
	Severity DiagnosisSeverity `json:"severity"`
	// Confidence is a normalized zero-to-one evidence confidence.
	Confidence float64 `json:"confidence"`
	// RootCauseScope identifies product, infrastructure, scenario, none, or unknown ownership.
	RootCauseScope RootCauseScope `json:"root_cause_scope"`
	// Summary is a compact message-free diagnosis.
	Summary string `json:"summary"`
	// ObservationReferences point to bounded MCP observations without raw evidence.
	ObservationReferences []DiagnosisObservationReference `json:"observation_references"`
	// SupportingSignals contains compact facts supporting the verdict.
	SupportingSignals []string `json:"supporting_signals"`
	// ContradictorySignals contains compact facts opposing the verdict.
	ContradictorySignals []string `json:"contradictory_signals"`
	// UnresolvedSignals contains evidence gaps that remain after analysis.
	UnresolvedSignals []string `json:"unresolved_signals"`
	// RemediationEligibility controls the isolated repository-write job.
	RemediationEligibility RemediationEligibility `json:"remediation_eligibility"`
	// ProposedRegressionCoverage lists bounded tests expected for a product fix.
	ProposedRegressionCoverage []string `json:"proposed_regression_coverage"`
	// CloudRevalidationRequired records whether a fix needs another live run.
	CloudRevalidationRequired bool `json:"cloud_revalidation_required"`
}

// DiagnosisRunIdentity binds a result to one immutable deployment contract.
type DiagnosisRunIdentity struct {
	// RunID is the exact Simulation Run identity.
	RunID string `json:"run_id"`
	// SourceSHA is the immutable deployed commit.
	SourceSHA string `json:"source_sha"`
	// ScenarioDigest is the canonical effective workload identity.
	ScenarioDigest string `json:"scenario_digest"`
}

// DiagnosisWindow records the exact live interval considered by Codex.
type DiagnosisWindow struct {
	// Start is the inclusive beginning of the analyzed interval.
	Start time.Time `json:"start"`
	// End is the inclusive end of the analyzed interval.
	End time.Time `json:"end"`
}

// DiagnosisObservationReference points to bounded MCP output without copying raw evidence.
type DiagnosisObservationReference struct {
	// Tool is the allowlisted Analysis MCP tool name.
	Tool string `json:"tool"`
	// Node is the bounded observation scope.
	Node string `json:"node"`
	// ObservedAt is the MCP observation completion time.
	ObservedAt time.Time `json:"observed_at"`
	// Window is the compact requested or observed interval.
	Window string `json:"window"`
	// Complete reports whether the referenced source was complete.
	Complete bool `json:"complete"`
	// State preserves workload_inspect's in_progress or completed state.
	State string `json:"state,omitempty"`
	// Status preserves workload_inspect's passed or failed terminal result.
	Status string `json:"status,omitempty"`
	// Note is a bounded non-sensitive reference caveat.
	Note string `json:"note,omitempty"`
}

// RemediationEligibility states whether a fresh job may attempt a repository-only fix.
type RemediationEligibility struct {
	// Eligible permits the isolated remediation job to consider a Draft PR.
	Eligible bool `json:"eligible"`
	// Reason is the compact gate rationale.
	Reason string `json:"reason"`
	// RepositoryAttributable confirms the defect is owned by repository code.
	RepositoryAttributable bool `json:"repository_attributable"`
	// Testable confirms deterministic regression coverage can be added.
	Testable bool `json:"testable"`
}

// DecodeDiagnosisResult strictly decodes and validates one bounded Diagnosis Result.
func DecodeDiagnosisResult(reader io.Reader) (DiagnosisResult, error) {
	if reader == nil {
		return DiagnosisResult{}, ErrInvalidDiagnosis
	}
	limited := &io.LimitedReader{R: reader, N: maxDiagnosisBytes + 1}
	data, err := io.ReadAll(limited)
	if err != nil {
		return DiagnosisResult{}, fmt.Errorf("%w: read: %v", ErrInvalidDiagnosis, err)
	}
	if limited.N <= 0 {
		return DiagnosisResult{}, fmt.Errorf("%w: result exceeds %d bytes", ErrInvalidDiagnosis, maxDiagnosisBytes)
	}
	if err := validateRequiredDiagnosisJSON(data); err != nil {
		return DiagnosisResult{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var result DiagnosisResult
	if err := decoder.Decode(&result); err != nil {
		return DiagnosisResult{}, fmt.Errorf("%w: decode: %v", ErrInvalidDiagnosis, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return DiagnosisResult{}, fmt.Errorf("%w: trailing data", ErrInvalidDiagnosis)
	}
	if err := result.Validate(); err != nil {
		return DiagnosisResult{}, err
	}
	return result, nil
}

func validateRequiredDiagnosisJSON(data []byte) error {
	invalid := func(reason string) error { return fmt.Errorf("%w: %s", ErrInvalidDiagnosis, reason) }
	decodeObject := func(raw []byte) (map[string]json.RawMessage, error) {
		if first := bytes.TrimSpace(raw); len(first) == 0 || first[0] != '{' {
			return nil, invalid("required object")
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil {
			return nil, invalid("required object")
		}
		return object, nil
	}
	require := func(object map[string]json.RawMessage, names ...string) error {
		for _, name := range names {
			raw, ok := object[name]
			if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				return invalid("missing required field " + name)
			}
		}
		return nil
	}
	requirePresent := func(object map[string]json.RawMessage, names ...string) error {
		for _, name := range names {
			if _, ok := object[name]; !ok {
				return invalid("missing required field " + name)
			}
		}
		return nil
	}
	requireNullableEnum := func(object map[string]json.RawMessage, name string, values ...string) error {
		raw := bytes.TrimSpace(object[name])
		if bytes.Equal(raw, []byte("null")) {
			return nil
		}
		var value string
		if err := json.Unmarshal(raw, &value); err != nil || !oneOf(value, values...) {
			return invalid("invalid nullable field " + name)
		}
		return nil
	}
	root, err := decodeObject(data)
	if err != nil {
		return err
	}
	if err := require(root, "schema", "run_identity", "analyzed_window", "verdict", "severity", "confidence", "root_cause_scope", "summary",
		"observation_references", "supporting_signals", "contradictory_signals", "unresolved_signals", "remediation_eligibility",
		"proposed_regression_coverage", "cloud_revalidation_required"); err != nil {
		return err
	}
	runIdentity, err := decodeObject(root["run_identity"])
	if err != nil {
		return err
	}
	if err := require(runIdentity, "run_id", "source_sha", "scenario_digest"); err != nil {
		return err
	}
	window, err := decodeObject(root["analyzed_window"])
	if err != nil {
		return err
	}
	if err := require(window, "start", "end"); err != nil {
		return err
	}
	eligibility, err := decodeObject(root["remediation_eligibility"])
	if err != nil {
		return err
	}
	if err := require(eligibility, "eligible", "reason", "repository_attributable", "testable"); err != nil {
		return err
	}
	var references []json.RawMessage
	if err := json.Unmarshal(root["observation_references"], &references); err != nil || references == nil {
		return invalid("observation_references array")
	}
	for _, raw := range references {
		reference, objectErr := decodeObject(raw)
		if objectErr != nil {
			return objectErr
		}
		if err := require(reference, "tool", "node", "observed_at", "window", "complete"); err != nil {
			return err
		}
		if err := requirePresent(reference, "state", "status", "note"); err != nil {
			return err
		}
		if err := requireNullableEnum(reference, "state", "in_progress", "completed"); err != nil {
			return err
		}
		if err := requireNullableEnum(reference, "status", "passed", "failed"); err != nil {
			return err
		}
	}
	for _, name := range []string{"supporting_signals", "contradictory_signals", "unresolved_signals", "proposed_regression_coverage"} {
		var values []string
		if err := json.Unmarshal(root[name], &values); err != nil || values == nil {
			return invalid(name + " array")
		}
	}
	return nil
}

// Validate enforces the schema's semantic bounds in ordinary Go tests and workflows.
func (d DiagnosisResult) Validate() error {
	invalid := func(reason string) error { return fmt.Errorf("%w: %s", ErrInvalidDiagnosis, reason) }
	if d.Schema != "wukongim/cloud-simulation-diagnosis/v1" {
		return invalid("schema")
	}
	if !boundedText(d.RunIdentity.RunID, 1, 128) || !diagnosisSHA.MatchString(d.RunIdentity.SourceSHA) || !diagnosisDigest.MatchString(d.RunIdentity.ScenarioDigest) {
		return invalid("run identity")
	}
	if d.AnalyzedWindow.Start.IsZero() || d.AnalyzedWindow.End.IsZero() || d.AnalyzedWindow.End.Before(d.AnalyzedWindow.Start) {
		return invalid("analyzed window")
	}
	if !oneOf(string(d.Verdict), string(VerdictHealthy), string(VerdictProductDefect), string(VerdictInfrastructureInterrupted), string(VerdictScenarioInvalid), string(VerdictInsufficientEvidence)) ||
		!oneOf(string(d.Severity), string(SeverityNone), string(SeverityLow), string(SeverityMedium), string(SeverityHigh), string(SeverityCritical)) || d.Confidence < 0 || d.Confidence > 1 ||
		!oneOf(string(d.RootCauseScope), string(RootCauseNone), string(RootCauseProduct), string(RootCauseInfrastructure), string(RootCauseScenario), string(RootCauseUnknown)) || !boundedText(d.Summary, 1, 2000) {
		return invalid("classification")
	}
	if len(d.ObservationReferences) > 32 {
		return invalid("observation references")
	}
	for _, ref := range d.ObservationReferences {
		if !boundedText(ref.Tool, 1, 64) || !boundedText(ref.Node, 1, 64) || ref.ObservedAt.IsZero() || !boundedText(ref.Window, 0, 256) || !boundedText(ref.Note, 0, 500) {
			return invalid("observation reference")
		}
		if ref.Tool == "workload_inspect" {
			switch ref.State {
			case "", "in_progress":
				if ref.Complete || ref.Status != "" {
					return invalid("workload observation reference")
				}
			case "completed":
				if !ref.Complete || ref.Status != "passed" && ref.Status != "failed" {
					return invalid("workload observation reference")
				}
			default:
				return invalid("workload observation reference")
			}
		} else if ref.State != "" || ref.Status != "" {
			return invalid("non-workload observation state")
		}
	}
	if !boundedDiagnosisStrings(d.SupportingSignals, 24, 500) || !boundedDiagnosisStrings(d.ContradictorySignals, 24, 500) || !boundedDiagnosisStrings(d.UnresolvedSignals, 24, 500) ||
		!boundedDiagnosisStrings(d.ProposedRegressionCoverage, 12, 500) || !boundedText(d.RemediationEligibility.Reason, 1, 1000) {
		return invalid("bounded text")
	}
	if d.RemediationEligibility.Eligible && (d.Verdict != VerdictProductDefect || d.RootCauseScope != RootCauseProduct || !d.RemediationEligibility.RepositoryAttributable || !d.RemediationEligibility.Testable || len(d.ProposedRegressionCoverage) == 0) {
		return invalid("remediation eligibility")
	}
	if d.RemediationEligibility.Eligible && !d.CloudRevalidationRequired {
		return invalid("eligible remediation requires cloud revalidation")
	}
	hasCompleteObservation := false
	hasCompleteWorkload := false
	for _, ref := range d.ObservationReferences {
		hasCompleteObservation = hasCompleteObservation || ref.Complete
		hasCompleteWorkload = hasCompleteWorkload || ref.Tool == "workload_inspect" && ref.Complete && ref.State == "completed" && ref.Status == "passed"
	}
	if d.Verdict != VerdictInsufficientEvidence && (len(d.ObservationReferences) == 0 || len(d.SupportingSignals) == 0 || !hasCompleteObservation) {
		return invalid("conclusive verdict requires complete supporting evidence")
	}
	switch d.Verdict {
	case VerdictHealthy:
		if d.Severity != SeverityNone || d.RootCauseScope != RootCauseNone || d.RemediationEligibility.Eligible || !hasCompleteWorkload {
			return invalid("healthy classification")
		}
	case VerdictProductDefect:
		if d.Severity == SeverityNone || d.RootCauseScope != RootCauseProduct {
			return invalid("product classification")
		}
	case VerdictInfrastructureInterrupted:
		if d.Severity == SeverityNone || d.RootCauseScope != RootCauseInfrastructure || d.RemediationEligibility.Eligible {
			return invalid("infrastructure classification")
		}
	case VerdictScenarioInvalid:
		if d.Severity == SeverityNone || d.RootCauseScope != RootCauseScenario || d.RemediationEligibility.Eligible {
			return invalid("scenario classification")
		}
	case VerdictInsufficientEvidence:
		if d.Severity != SeverityNone || d.RootCauseScope != RootCauseUnknown || d.RemediationEligibility.Eligible {
			return invalid("insufficient evidence classification")
		}
	}
	return nil
}

func boundedDiagnosisStrings(values []string, maximum, textMaximum int) bool {
	if len(values) > maximum {
		return false
	}
	for _, value := range values {
		if !boundedText(value, 1, textMaximum) {
			return false
		}
	}
	return true
}

func boundedText(value string, minimum, maximum int) bool {
	if !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return false
	}
	length := utf8.RuneCountInString(value)
	return length >= minimum && length <= maximum
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}
