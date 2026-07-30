package reviewagent

import (
	"errors"
	"io"
)

const (
	MaxSummaryBytes    = 4096
	MaxFindings        = 100
	MaxFileAssessments = MaxChangedFiles
	MaxFindingEvidence = 32
	MaxSources         = 256
	MaxInlineComments  = 20
)

// Decision is the only advisory decision a model may return.
type Decision string

const (
	DecisionApproved        Decision = "approved"
	DecisionChangesRequired Decision = "changes_required"
	DecisionInconclusive    Decision = "inconclusive"
)

// FindingKind separates blocking defects from advisory suggestions.
type FindingKind string

const (
	FindingBlocking FindingKind = "blocking"
	FindingAdvisory FindingKind = "advisory"
)

// ReviewDimension identifies the design axis supporting a finding.
type ReviewDimension string

const (
	DimensionIntentCorrectness ReviewDimension = "intent_correctness"
	DimensionRegressionTests   ReviewDimension = "regression_tests"
	DimensionSecurityRuntime   ReviewDimension = "security_runtime"
	DimensionRepositoryRules   ReviewDimension = "repository_constraints"
)

// FileRisk is the model's bounded risk classification for one inventory item.
type FileRisk string

const (
	FileRiskLow    FileRisk = "low"
	FileRiskMedium FileRisk = "medium"
	FileRiskHigh   FileRisk = "high"
)

// FileAssessment proves that the model classified one changed path.
type FileAssessment struct {
	Path    string   `json:"path"`
	Risk    FileRisk `json:"risk"`
	Summary string   `json:"summary"`
}

// Finding describes one concrete review concern or bounded suggestion.
type Finding struct {
	Kind       FindingKind     `json:"kind"`
	Dimension  ReviewDimension `json:"dimension"`
	Title      string          `json:"title"`
	Path       string          `json:"path"`
	LineStart  uint64          `json:"line_start"`
	LineEnd    uint64          `json:"line_end"`
	Scenario   string          `json:"scenario"`
	Impact     string          `json:"impact"`
	Evidence   []string        `json:"evidence"`
	Resolution string          `json:"resolution"`
}

// ReviewResult is untrusted model output. It deliberately has no Check,
// Review, comment, merge, branch, commit, or state publication authority.
type ReviewResult struct {
	SchemaVersion         int                `json:"schema_version"`
	Generation            GenerationIdentity `json:"generation"`
	Decision              Decision           `json:"decision"`
	Summary               string             `json:"summary"`
	InventoryComplete     bool               `json:"inventory_complete"`
	FileAssessments       []FileAssessment   `json:"file_assessments"`
	Findings              []Finding          `json:"findings"`
	Sources               []string           `json:"sources"`
	UnresolvedUncertainty string             `json:"unresolved_uncertainty"`
}

// DecodeReviewResult strictly decodes one bounded advisory model response.
func DecodeReviewResult(reader io.Reader, maxBytes int64) (ReviewResult, error) {
	var result ReviewResult
	if err := decodeStrictJSON(reader, maxBytes, &result); err != nil {
		return ReviewResult{}, err
	}
	if err := ValidateReviewResult(result); err != nil {
		return ReviewResult{}, err
	}
	return result, nil
}

// ValidateReviewResult rejects incomplete, contradictory, or unbounded model
// output before trusted orchestration considers it.
func ValidateReviewResult(result ReviewResult) error {
	if result.SchemaVersion != 1 {
		return errors.New("unsupported Review result schema version")
	}
	if err := ValidateGenerationIdentity(result.Generation); err != nil {
		return err
	}
	switch result.Decision {
	case DecisionApproved, DecisionChangesRequired, DecisionInconclusive:
	default:
		return errors.New("invalid Review decision")
	}
	if !validText(result.Summary, MaxSummaryBytes, true) ||
		!validText(result.UnresolvedUncertainty, MaxSummaryBytes, false) {
		return errors.New("invalid Review result summary")
	}
	if !result.InventoryComplete {
		return errors.New("Review result does not cover the complete inventory")
	}
	if len(result.FileAssessments) == 0 ||
		len(result.FileAssessments) > MaxFileAssessments {
		return errors.New("invalid Review result file assessments")
	}
	paths := make(map[string]struct{}, len(result.FileAssessments))
	for _, assessment := range result.FileAssessments {
		if err := validateFileAssessment(assessment); err != nil {
			return err
		}
		if _, exists := paths[assessment.Path]; exists {
			return errors.New("duplicate Review result file assessment")
		}
		paths[assessment.Path] = struct{}{}
	}
	if len(result.Findings) > MaxFindings {
		return errors.New("Review result contains too many findings")
	}
	blocking := 0
	for _, finding := range result.Findings {
		if err := validateFinding(finding); err != nil {
			return err
		}
		if finding.Kind == FindingBlocking {
			blocking++
		}
	}
	if !validUniqueStrings(result.Sources, MaxSources, 2048, false) {
		return errors.New("invalid Review result sources")
	}
	switch result.Decision {
	case DecisionApproved:
		if blocking != 0 || result.UnresolvedUncertainty != "" {
			return errors.New("approved Review result contains blocking risk")
		}
	case DecisionChangesRequired:
		if blocking == 0 {
			return errors.New("changes-required Review result lacks a blocking finding")
		}
	case DecisionInconclusive:
		if result.UnresolvedUncertainty == "" {
			return errors.New("inconclusive Review result lacks uncertainty")
		}
	}
	return nil
}

// ReviewResultDigest binds the full advisory result without granting it
// authority.
func ReviewResultDigest(result ReviewResult) (string, error) {
	if err := ValidateReviewResult(result); err != nil {
		return "", err
	}
	return canonicalDigest(result, "encode Review result")
}

func validateFileAssessment(assessment FileAssessment) error {
	if !validRepositoryPath(assessment.Path) ||
		!validText(assessment.Summary, 2048, true) {
		return errors.New("invalid Review file assessment")
	}
	switch assessment.Risk {
	case FileRiskLow, FileRiskMedium, FileRiskHigh:
		return nil
	default:
		return errors.New("invalid Review file risk")
	}
}

func validateFinding(finding Finding) error {
	switch finding.Kind {
	case FindingBlocking, FindingAdvisory:
	default:
		return errors.New("invalid Review finding kind")
	}
	switch finding.Dimension {
	case DimensionIntentCorrectness,
		DimensionRegressionTests,
		DimensionSecurityRuntime,
		DimensionRepositoryRules:
	default:
		return errors.New("invalid Review finding dimension")
	}
	if !validText(finding.Title, 512, true) ||
		!validText(finding.Scenario, 4096, true) ||
		!validText(finding.Impact, 4096, true) ||
		!validText(finding.Resolution, 4096, true) ||
		!validUniqueStrings(
			finding.Evidence,
			MaxFindingEvidence,
			2048,
			true,
		) {
		return errors.New("invalid Review finding detail")
	}
	if finding.Path == "" {
		if finding.LineStart != 0 || finding.LineEnd != 0 {
			return errors.New("unlocated Review finding contains lines")
		}
		return nil
	}
	if !validRepositoryPath(finding.Path) ||
		finding.LineStart == 0 ||
		finding.LineEnd < finding.LineStart {
		return errors.New("invalid Review finding location")
	}
	return nil
}
