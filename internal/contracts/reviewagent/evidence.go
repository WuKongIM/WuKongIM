package reviewagent

import (
	"errors"
	"io"
	"time"
)

const MaxChecks = 128
const MaxCheckOutputExcerptBytes = 64 << 10

// CheckOutcome is a trusted named-check runner conclusion.
type CheckOutcome string

const (
	CheckOutcomePassed CheckOutcome = "passed"
	CheckOutcomeFailed CheckOutcome = "failed"
	CheckOutcomeError  CheckOutcome = "error"
)

// CheckEvidence records one fixed catalog command without exposing a
// caller-controlled command string.
type CheckEvidence struct {
	Name          string       `json:"name"`
	CommandDigest string       `json:"command_digest"`
	Outcome       CheckOutcome `json:"outcome"`
	ExitCode      int          `json:"exit_code"`
	DurationMS    uint64       `json:"duration_ms"`
	StdoutDigest  string       `json:"stdout_digest"`
	StderrDigest  string       `json:"stderr_digest"`
	Stdout        string       `json:"stdout"`
	Stderr        string       `json:"stderr"`
}

// ReviewEvidence is trusted output from the named-check boundary.
type ReviewEvidence struct {
	SchemaVersion int                `json:"schema_version"`
	Generation    GenerationIdentity `json:"generation"`
	Complete      bool               `json:"complete"`
	Checks        []CheckEvidence    `json:"checks"`
	FailureReason string             `json:"failure_reason"`
	CreatedAt     time.Time          `json:"created_at"`
}

// ValidateReviewEvidence validates exact-generation trusted evidence.
func ValidateReviewEvidence(evidence ReviewEvidence) error {
	if evidence.SchemaVersion != 1 {
		return errors.New("unsupported Review evidence schema version")
	}
	if err := ValidateGenerationIdentity(evidence.Generation); err != nil {
		return err
	}
	if len(evidence.Checks) == 0 || len(evidence.Checks) > MaxChecks {
		return errors.New("invalid Review evidence checks")
	}
	names := make(map[string]struct{}, len(evidence.Checks))
	for _, check := range evidence.Checks {
		if err := validateCheckEvidence(check); err != nil {
			return err
		}
		if _, exists := names[check.Name]; exists {
			return errors.New("duplicate Review evidence check")
		}
		names[check.Name] = struct{}{}
	}
	if evidence.Complete {
		if evidence.FailureReason != "" {
			return errors.New("complete Review evidence contains a failure")
		}
	} else if !validText(evidence.FailureReason, 4096, true) {
		return errors.New("incomplete Review evidence lacks a reason")
	}
	if evidence.CreatedAt.IsZero() || evidence.CreatedAt.Location() != time.UTC {
		return errors.New("Review evidence timestamp must use UTC")
	}
	return nil
}

// DecodeReviewEvidence decodes one bounded trusted evidence document.
func DecodeReviewEvidence(
	reader io.Reader,
	maxBytes int64,
) (ReviewEvidence, error) {
	var evidence ReviewEvidence
	if err := decodeStrictJSON(reader, maxBytes, &evidence); err != nil {
		return ReviewEvidence{}, err
	}
	if err := ValidateReviewEvidence(evidence); err != nil {
		return ReviewEvidence{}, err
	}
	return evidence, nil
}

// ReviewEvidenceDigest binds every named-check outcome.
func ReviewEvidenceDigest(evidence ReviewEvidence) (string, error) {
	if err := ValidateReviewEvidence(evidence); err != nil {
		return "", err
	}
	return canonicalDigest(evidence, "encode Review evidence")
}

func validateCheckEvidence(check CheckEvidence) error {
	if !checkNamePattern.MatchString(check.Name) ||
		!validDigest(check.CommandDigest) ||
		!validDigest(check.StdoutDigest) ||
		!validDigest(check.StderrDigest) ||
		!validText(check.Stdout, MaxCheckOutputExcerptBytes, false) ||
		!validText(check.Stderr, MaxCheckOutputExcerptBytes, false) ||
		check.DurationMS == 0 {
		return errors.New("invalid Review check evidence")
	}
	switch check.Outcome {
	case CheckOutcomePassed:
		if check.ExitCode != 0 {
			return errors.New("passing Review check has nonzero exit code")
		}
	case CheckOutcomeFailed:
		if check.ExitCode == 0 {
			return errors.New("failed Review check has zero exit code")
		}
	case CheckOutcomeError:
	default:
		return errors.New("invalid Review check outcome")
	}
	return nil
}
