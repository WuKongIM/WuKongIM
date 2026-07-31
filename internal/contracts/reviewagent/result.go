package reviewagent

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

const (
	MaxSummaryBytes         = 2048
	MaxFindings             = 8
	MaxFileAssessments      = MaxChangedFiles
	MaxResultPathBytes      = 1024
	MaxFindingTitleBytes    = 256
	MaxFindingDetailBytes   = 1024
	MaxFindingEvidence      = 4
	MaxFindingEvidenceBytes = 512
	MaxSources              = 256
	MaxSourceBytes          = 2048
	MaxInlineComments       = 20
	// MaxPersistedFindingsBytes keeps any validated model finding set safely
	// below the signed Review state storage bound.
	MaxPersistedFindingsBytes = 64 << 10
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

// PriorFindingDisposition proves that a new review explicitly retained or
// withdrew every finding carried from an earlier generation.
type PriorFindingDisposition struct {
	FindingDigest string `json:"finding_digest"`
	Status        string `json:"status"`
	Reason        string `json:"reason"`
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

// ValidateFinding validates one bounded structured finding outside a complete
// model result, such as findings frozen into signed Review state.
func ValidateFinding(finding Finding) error {
	return validateFinding(finding)
}

// ReviewResult is untrusted model output. It deliberately has no Check,
// Review, comment, merge, branch, commit, or state publication authority.
type ReviewResult struct {
	SchemaVersion            int                       `json:"schema_version"`
	Generation               GenerationIdentity        `json:"generation"`
	Decision                 Decision                  `json:"decision"`
	Summary                  string                    `json:"summary"`
	InventoryComplete        bool                      `json:"inventory_complete"`
	FileAssessments          []FileAssessment          `json:"file_assessments"`
	Findings                 []Finding                 `json:"findings"`
	PriorFindingDispositions []PriorFindingDisposition `json:"prior_finding_dispositions"`
	Sources                  []string                  `json:"sources"`
	UnresolvedUncertainty    string                    `json:"unresolved_uncertainty"`
}

// DecodeReviewResult decodes one bounded advisory model response. It accepts
// strict JSON directly or one unambiguous JSON object wrapped in model prose.
func DecodeReviewResult(reader io.Reader, maxBytes int64) (ReviewResult, error) {
	body, err := readBoundedJSON(reader, maxBytes)
	if err != nil {
		return ReviewResult{}, err
	}
	var result ReviewResult
	rawErr := decodeStrictJSON(bytes.NewReader(body), maxBytes, &result)
	if rawErr != nil {
		object, ok := extractSingleJSONObject(body)
		if !ok {
			return ReviewResult{}, rawErr
		}
		result = ReviewResult{}
		if err := decodeStrictJSON(
			bytes.NewReader(object),
			maxBytes,
			&result,
		); err != nil {
			return ReviewResult{}, err
		}
	}
	if err := ValidateReviewResult(result); err != nil {
		return ReviewResult{}, err
	}
	return result, nil
}

// extractSingleJSONObject rejects competing JSON containers while allowing a
// model to surround its only structured result with bounded prose or a fence.
func extractSingleJSONObject(body []byte) ([]byte, bool) {
	start := bytes.IndexByte(body, '{')
	end := bytes.LastIndexByte(body, '}')
	if start < 0 || end <= start {
		return nil, false
	}
	if bytes.ContainsAny(body[:start], "{}[]") ||
		bytes.ContainsAny(body[end+1:], "{}[]") {
		return nil, false
	}
	return bytes.TrimSpace(body[start : end+1]), true
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
	dispositions := make(map[string]struct{}, len(result.PriorFindingDispositions))
	if len(result.PriorFindingDispositions) > MaxFindings {
		return errors.New("too many prior Review finding dispositions")
	}
	for _, disposition := range result.PriorFindingDispositions {
		if !validDigest(disposition.FindingDigest) ||
			(disposition.Status != "retained" &&
				disposition.Status != "withdrawn") ||
			!validText(disposition.Reason, MaxSummaryBytes, true) {
			return errors.New("invalid prior Review finding disposition")
		}
		if _, duplicate := dispositions[disposition.FindingDigest]; duplicate {
			return errors.New("duplicate prior Review finding disposition")
		}
		dispositions[disposition.FindingDigest] = struct{}{}
	}
	findingsBody, err := json.Marshal(result.Findings)
	if err != nil || len(findingsBody) > MaxPersistedFindingsBytes {
		return errors.New("Review result findings exceed persisted byte budget")
	}
	if !validUniqueStrings(
		result.Sources,
		MaxSources,
		MaxSourceBytes,
		false,
	) {
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

// FindingDigest returns the stable identity used to reconcile a prior finding.
func FindingDigest(finding Finding) (string, error) {
	if err := ValidateFinding(finding); err != nil {
		return "", err
	}
	return canonicalDigest(finding, "encode Review finding")
}

// ValidatePriorFindingDispositions requires one exact, explicit disposition
// for every finding in the trusted review context.
func ValidatePriorFindingDispositions(
	prior []Finding,
	result ReviewResult,
) error {
	if len(prior) != len(result.PriorFindingDispositions) {
		return errors.New("Review result does not disposition every prior finding")
	}
	current := make(map[string]struct{}, len(result.Findings))
	for _, finding := range result.Findings {
		digest, err := FindingDigest(finding)
		if err != nil {
			return err
		}
		current[digest] = struct{}{}
	}
	dispositions := make(map[string]PriorFindingDisposition, len(
		result.PriorFindingDispositions,
	))
	for _, disposition := range result.PriorFindingDispositions {
		dispositions[disposition.FindingDigest] = disposition
	}
	for _, finding := range prior {
		digest, err := FindingDigest(finding)
		if err != nil {
			return err
		}
		disposition, exists := dispositions[digest]
		if !exists {
			return errors.New("Review result omits a prior finding disposition")
		}
		_, retained := current[digest]
		switch disposition.Status {
		case "retained":
			if !retained {
				return errors.New("retained prior finding is absent from findings")
			}
		case "withdrawn":
			if retained {
				return errors.New("withdrawn prior finding remains in findings")
			}
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
		len(assessment.Path) > MaxResultPathBytes ||
		!validText(assessment.Summary, MaxSummaryBytes, true) {
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
	if !validText(finding.Title, MaxFindingTitleBytes, true) ||
		!validText(finding.Scenario, MaxFindingDetailBytes, true) ||
		!validText(finding.Impact, MaxFindingDetailBytes, true) ||
		!validText(finding.Resolution, MaxFindingDetailBytes, true) ||
		!validUniqueStrings(
			finding.Evidence,
			MaxFindingEvidence,
			MaxFindingEvidenceBytes,
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
		len(finding.Path) > MaxResultPathBytes ||
		finding.LineStart == 0 ||
		finding.LineEnd < finding.LineStart {
		return errors.New("invalid Review finding location")
	}
	return nil
}
