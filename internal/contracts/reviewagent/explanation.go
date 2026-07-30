package reviewagent

import (
	"errors"
	"io"
	"strings"
)

// ExplanationResult is a bounded advisory answer about one signed Review
// generation. It cannot alter the decision or any trusted evidence.
type ExplanationResult struct {
	SchemaVersion int                `json:"schema_version"`
	Generation    GenerationIdentity `json:"generation"`
	Reply         string             `json:"reply"`
}

// MaxExplanationReplyBytes leaves room for the fixed idempotency marker under
// GitHub's Issue-comment body limit.
const MaxExplanationReplyBytes = 60 << 10

// DecodeExplanationResult strictly decodes one bounded model reply.
func DecodeExplanationResult(
	reader io.Reader,
	maxBytes int64,
) (ExplanationResult, error) {
	var result ExplanationResult
	if err := decodeStrictJSON(reader, maxBytes, &result); err != nil {
		return ExplanationResult{}, err
	}
	if err := ValidateExplanationResult(result); err != nil {
		return ExplanationResult{}, err
	}
	return result, nil
}

// ValidateExplanationResult rejects replies detached from a valid generation.
func ValidateExplanationResult(result ExplanationResult) error {
	if result.SchemaVersion != 1 {
		return errors.New("unsupported Review explanation schema version")
	}
	if err := ValidateGenerationIdentity(result.Generation); err != nil {
		return err
	}
	if !validText(result.Reply, MaxExplanationReplyBytes, true) ||
		strings.Contains(result.Reply, "<!-- review-agent-") {
		return errors.New("invalid Review explanation reply")
	}
	return nil
}

// ExplanationResultDigest binds the exact advisory reply.
func ExplanationResultDigest(result ExplanationResult) (string, error) {
	if err := ValidateExplanationResult(result); err != nil {
		return "", err
	}
	return canonicalDigest(result, "encode Review explanation")
}
