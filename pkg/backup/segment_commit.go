package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	// SegmentCommitFormat identifies a signed replicated segment commit proof.
	SegmentCommitFormat = "wukongim-backup-segment-commit"
	// SegmentCommitVersion is the current signed segment commit schema version.
	SegmentCommitVersion uint32 = 1

	maxSegmentCommitBytes = 64 << 10
)

// SegmentCommit is the signed visibility proof for one encrypted segment.
type SegmentCommit struct {
	// Format must equal SegmentCommitFormat.
	Format string `json:"format"`
	// Version selects the segment commit schema.
	Version uint32 `json:"version"`
	// SegmentID is the lowercase SHA-256 of Header.
	SegmentID string `json:"segment_id"`
	// Header is the stable logical segment identity.
	Header SegmentHeader `json:"header"`
	// Payload identifies one encrypted representation committed in both repositories.
	Payload SegmentPayload `json:"payload"`
	// PrimaryRepository is the operator-facing primary failure-domain identity.
	PrimaryRepository string `json:"primary_repository"`
	// SecondaryRepository is the operator-facing secondary failure-domain identity.
	SecondaryRepository string `json:"secondary_repository"`
	// Signature authenticates every preceding field.
	Signature *ManifestSignature `json:"signature,omitempty"`
}

// SignSegmentCommit validates and signs a copy of commit.
func SignSegmentCommit(
	ctx context.Context,
	commit SegmentCommit,
	signer ManifestSigner,
) (SegmentCommit, error) {
	if signer == nil {
		return SegmentCommit{}, fmt.Errorf("%w: segment signer is required", ErrInvalidObject)
	}
	commit.Signature = nil
	canonical, err := canonicalSegmentCommit(commit)
	if err != nil {
		return SegmentCommit{}, err
	}
	signature, err := signer.Sign(ctx, canonical)
	if err != nil {
		return SegmentCommit{}, fmt.Errorf("%w: sign segment commit: %v", ErrInvalidSignature, err)
	}
	if strings.TrimSpace(signature.KeyID) == "" ||
		strings.TrimSpace(signature.Algorithm) == "" ||
		len(signature.Value) == 0 {
		return SegmentCommit{}, fmt.Errorf("%w: segment signer metadata mismatch", ErrInvalidSignature)
	}
	commit.Signature = &signature
	if err := validateSegmentCommit(commit, true); err != nil {
		return SegmentCommit{}, err
	}
	return commit, nil
}

// MarshalSegmentCommit validates and serializes one signed segment commit.
func MarshalSegmentCommit(commit SegmentCommit) ([]byte, error) {
	if err := validateSegmentCommit(commit, true); err != nil {
		return nil, err
	}
	body, err := json.Marshal(commit)
	if err != nil {
		return nil, fmt.Errorf("marshal segment commit: %w", err)
	}
	if len(body) > maxSegmentCommitBytes {
		return nil, fmt.Errorf("%w: segment commit exceeds size limit", ErrInvalidObject)
	}
	return body, nil
}

// LoadSegmentCommit strictly decodes, validates, and verifies a signed commit.
func LoadSegmentCommit(ctx context.Context, body []byte, signer ManifestSigner) (SegmentCommit, error) {
	if signer == nil {
		return SegmentCommit{}, fmt.Errorf("%w: segment verifier is required", ErrInvalidObject)
	}
	if len(body) == 0 || len(body) > maxSegmentCommitBytes {
		return SegmentCommit{}, fmt.Errorf("%w: segment commit size is outside bounds", ErrInvalidObject)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var commit SegmentCommit
	if err := decoder.Decode(&commit); err != nil {
		return SegmentCommit{}, fmt.Errorf("%w: decode segment commit: %v", ErrInvalidObject, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return SegmentCommit{}, fmt.Errorf("%w: trailing segment commit data", ErrInvalidObject)
	}
	if err := validateSegmentCommit(commit, true); err != nil {
		return SegmentCommit{}, err
	}
	signature := *commit.Signature
	canonical, err := canonicalSegmentCommit(commit)
	if err != nil {
		return SegmentCommit{}, err
	}
	if err := signer.Verify(ctx, signature, canonical); err != nil {
		return SegmentCommit{}, fmt.Errorf("%w: verify segment commit: %v", ErrInvalidSignature, err)
	}
	return commit, nil
}

func canonicalSegmentCommit(commit SegmentCommit) ([]byte, error) {
	commit.Signature = nil
	if err := validateSegmentCommit(commit, false); err != nil {
		return nil, err
	}
	body, err := json.Marshal(commit)
	if err != nil {
		return nil, fmt.Errorf("canonical segment commit: %w", err)
	}
	return body, nil
}

func validateSegmentCommit(commit SegmentCommit, requireSignature bool) error {
	if commit.Format != SegmentCommitFormat || commit.Version != SegmentCommitVersion {
		return fmt.Errorf("%w: segment commit format or version is unsupported", ErrInvalidObject)
	}
	if err := validateSHA256(commit.SegmentID); err != nil {
		return fmt.Errorf("%w: segment id: %v", ErrInvalidObject, err)
	}
	headerBody, err := canonicalSegmentHeader(commit.Header)
	if err != nil {
		return err
	}
	headerHash := sha256.Sum256(headerBody)
	if commit.SegmentID != hex.EncodeToString(headerHash[:]) {
		return fmt.Errorf("%w: segment id does not match header", ErrInvalidObject)
	}
	if err := validateSegmentPayload(commit.SegmentID, commit.Payload); err != nil {
		return err
	}
	primary := strings.TrimSpace(commit.PrimaryRepository)
	secondary := strings.TrimSpace(commit.SecondaryRepository)
	if primary == "" || secondary == "" || primary == secondary || len(primary) > 128 || len(secondary) > 128 {
		return fmt.Errorf("%w: segment commit repositories are invalid", ErrInvalidObject)
	}
	if requireSignature {
		if commit.Signature == nil || strings.TrimSpace(commit.Signature.KeyID) == "" || strings.TrimSpace(commit.Signature.Algorithm) == "" || len(commit.Signature.Value) == 0 {
			return fmt.Errorf("%w: segment commit signature is required", ErrInvalidSignature)
		}
	} else if commit.Signature != nil {
		return fmt.Errorf("%w: unsigned segment commit contains a signature", ErrInvalidObject)
	}
	return nil
}

func segmentCommitKey(segmentID string) string {
	return "segments/" + segmentID + "/commit.json"
}
