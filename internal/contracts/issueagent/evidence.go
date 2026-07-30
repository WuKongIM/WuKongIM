package issueagent

import (
	"errors"
	"io"
	"time"
)

// CandidateRisk is the Verifier's trusted publication risk class.
type CandidateRisk string

const (
	CandidateRiskLow           CandidateRisk = "low"
	CandidateRiskInvestigation CandidateRisk = "investigation_only"
	CandidateRiskHigh          CandidateRisk = "high"
)

// VerificationCommand records one trusted Verifier command.
type VerificationCommand struct {
	Arguments    []string `json:"arguments"`
	WorkingDir   string   `json:"working_dir"`
	ExitCode     int      `json:"exit_code"`
	StdoutDigest string   `json:"stdout_digest"`
	StderrDigest string   `json:"stderr_digest"`
	DurationMS   uint64   `json:"duration_ms"`
}

// CandidateEvidence is the only trusted publication and test authority.
type CandidateEvidence struct {
	SchemaVersion       int                   `json:"schema_version"`
	Repository          string                `json:"repository"`
	IssueNumber         int64                 `json:"issue_number"`
	TaskID              string                `json:"task_id"`
	BaseSHA             string                `json:"base_sha"`
	CandidateDigest     string                `json:"candidate_digest"`
	ChangeSetDigest     string                `json:"change_set_digest"`
	Risk                CandidateRisk         `json:"risk"`
	PublicationEligible bool                  `json:"publication_eligible"`
	RequiredSuites      []string              `json:"required_suites"`
	Commands            []VerificationCommand `json:"commands"`
	FailureReason       string                `json:"failure_reason"`
	CreatedAt           time.Time             `json:"created_at"`
}

// DecodeCandidateEvidence decodes one strict bounded Verifier decision.
func DecodeCandidateEvidence(
	reader io.Reader,
	maxBytes int64,
) (CandidateEvidence, error) {
	var evidence CandidateEvidence
	if err := decodeStrictJSON(reader, maxBytes, &evidence); err != nil {
		return CandidateEvidence{}, err
	}
	if err := ValidateCandidateEvidence(evidence); err != nil {
		return CandidateEvidence{}, err
	}
	return evidence, nil
}

// ValidateCandidateEvidence validates the Verifier-authored trust boundary.
func ValidateCandidateEvidence(evidence CandidateEvidence) error {
	if evidence.SchemaVersion != 2 ||
		!validRepository(evidence.Repository) ||
		evidence.IssueNumber <= 0 ||
		!digestPattern.MatchString(evidence.TaskID) ||
		!gitSHAPattern.MatchString(evidence.BaseSHA) ||
		!digestPattern.MatchString(evidence.CandidateDigest) ||
		!digestPattern.MatchString(evidence.ChangeSetDigest) {
		return errors.New("invalid Candidate Evidence identity")
	}
	switch evidence.Risk {
	case CandidateRiskLow, CandidateRiskInvestigation, CandidateRiskHigh:
	default:
		return errors.New("invalid Candidate Evidence risk")
	}
	if len(evidence.RequiredSuites) == 0 ||
		!strictContextStrings(evidence.RequiredSuites, 256, true) ||
		len(evidence.Commands) > maxContextItems {
		return errors.New("invalid Candidate Evidence test plan")
	}
	if evidence.PublicationEligible && len(evidence.Commands) == 0 {
		return errors.New("publishable Candidate Evidence lacks commands")
	}
	for _, command := range evidence.Commands {
		if err := validateVerificationCommand(command); err != nil {
			return err
		}
		if evidence.PublicationEligible && command.ExitCode != 0 {
			return errors.New("publishable Candidate Evidence contains a failed command")
		}
	}
	if evidence.PublicationEligible {
		if evidence.Risk != CandidateRiskLow {
			return errors.New("only low-risk Candidate Evidence is publishable")
		}
		if evidence.FailureReason != "" {
			return errors.New("publishable Candidate Evidence contains a failure")
		}
	} else if !validContextText(evidence.FailureReason, 4096, true) {
		return errors.New("rejected Candidate Evidence requires a reason")
	}
	if evidence.CreatedAt.IsZero() || evidence.CreatedAt.Location() != time.UTC {
		return errors.New("Candidate Evidence timestamp must use UTC")
	}
	return nil
}

// CandidateEvidenceDigest binds the Verifier's complete trusted decision.
func CandidateEvidenceDigest(evidence CandidateEvidence) (string, error) {
	if err := ValidateCandidateEvidence(evidence); err != nil {
		return "", err
	}
	return canonicalDigest(evidence, "encode Candidate Evidence")
}

func validateVerificationCommand(command VerificationCommand) error {
	if len(command.Arguments) == 0 || len(command.Arguments) > 128 ||
		!boundedResultStrings(command.Arguments, 4096, false) ||
		command.DurationMS == 0 ||
		!digestPattern.MatchString(command.StdoutDigest) ||
		!digestPattern.MatchString(command.StderrDigest) {
		return errors.New("invalid verification command")
	}
	if command.WorkingDir != "." {
		if validateRepositoryPath(command.WorkingDir) != nil {
			return errors.New("invalid verification working directory")
		}
	}
	return nil
}
