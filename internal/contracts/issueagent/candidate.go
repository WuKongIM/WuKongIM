package issueagent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
)

// DecodeCandidateSnapshot decodes one strict bounded candidate.
func DecodeCandidateSnapshot(
	reader io.Reader,
	maxBytes int64,
) (CandidateSnapshot, error) {
	var snapshot CandidateSnapshot
	if err := decodeStrictJSON(reader, maxBytes, &snapshot); err != nil {
		return CandidateSnapshot{}, err
	}
	if err := ValidateCandidateSnapshot(snapshot); err != nil {
		return CandidateSnapshot{}, err
	}
	return snapshot, nil
}

// CandidateSnapshot is the trusted filesystem-derived Engineer output.
type CandidateSnapshot struct {
	SchemaVersion int       `json:"schema_version"`
	TaskID        string    `json:"task_id"`
	BaseSHA       string    `json:"base_sha"`
	ChangeSet     ChangeSet `json:"change_set"`
}

// ValidateCandidateSnapshot validates a captured cross-job candidate.
func ValidateCandidateSnapshot(snapshot CandidateSnapshot) error {
	if snapshot.SchemaVersion != 2 ||
		!digestPattern.MatchString(snapshot.TaskID) ||
		!gitSHAPattern.MatchString(snapshot.BaseSHA) {
		return errors.New("invalid Candidate Snapshot identity")
	}
	return ValidateChangeSet(snapshot.ChangeSet, PublisherChangeSetLimits())
}

// CandidateSnapshotDigest binds the complete captured ChangeSet.
func CandidateSnapshotDigest(snapshot CandidateSnapshot) (string, error) {
	if err := ValidateCandidateSnapshot(snapshot); err != nil {
		return "", err
	}
	return canonicalDigest(snapshot, "encode Candidate Snapshot")
}

// ChangeSetDigest binds the exact sorted file operations and contents.
func ChangeSetDigest(changeSet ChangeSet) (string, error) {
	if err := ValidateChangeSet(
		changeSet, PublisherChangeSetLimits(),
	); err != nil {
		return "", err
	}
	return canonicalDigest(changeSet, "encode candidate ChangeSet")
}

func canonicalDigest(value any, message string) (string, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return "", errors.New(message)
	}
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
