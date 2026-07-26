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
	// SourceFenceReceiptFormat identifies a signed source-cluster write fence.
	SourceFenceReceiptFormat = "wukongim-backup-source-fence"
	// SourceFenceReceiptVersion is the current source-fence receipt schema.
	SourceFenceReceiptVersion uint32 = 1

	maxSourceFenceReceiptBytes = 64 << 10
)

// SourceFenceRecord is the immutable Controller-resident intent that disables
// ordinary work on one exact source cluster generation.
type SourceFenceRecord struct {
	// Format and Version select the signed receipt schema.
	Format  string `json:"format"`
	Version uint32 `json:"version"`
	// ID identifies this one-way source fence.
	ID string `json:"id"`
	// SourceClusterID and SourceGeneration identify the fenced incarnation.
	SourceClusterID  string `json:"source_cluster_id"`
	SourceGeneration string `json:"source_generation"`
	// RestorePlanID binds the fence to one immutable successor restore plan.
	RestorePlanID string `json:"restore_plan_id"`
	// CheckpointID and CheckpointSHA256 bind the exact restored checkpoint.
	CheckpointID     string `json:"checkpoint_id"`
	CheckpointSHA256 string `json:"checkpoint_sha256"`
	// TargetClusterID and TargetGeneration identify the intended successor.
	TargetClusterID  string `json:"target_cluster_id"`
	TargetGeneration string `json:"target_generation"`
	// FenceControllerRevision is the first Controller revision carrying this fence.
	FenceControllerRevision uint64 `json:"fence_controller_revision"`
	// RequestedAtUnixMillis is when the irreversible fence was accepted.
	RequestedAtUnixMillis int64 `json:"requested_at_unix_millis"`
	// ConvergedAtUnixMillis is set only after every active data node reports
	// observing FenceControllerRevision.
	ConvergedAtUnixMillis int64 `json:"converged_at_unix_millis,omitempty"`
}

// SourceFenceReceipt is the KMS-signed proof issued after the source fence
// converges across every active data node.
type SourceFenceReceipt struct {
	SourceFenceRecord
	// Signature authenticates every field in SourceFenceRecord.
	Signature *ManifestSignature `json:"signature,omitempty"`
}

// ValidateSourceFenceRecord validates a Controller-resident source fence.
// requireConverged distinguishes the durable requested and converged phases.
func ValidateSourceFenceRecord(record SourceFenceRecord, requireConverged bool) error {
	if record.Format != SourceFenceReceiptFormat ||
		record.Version != SourceFenceReceiptVersion {
		return fmt.Errorf("%w: source fence format or version is unsupported", ErrInvalidObject)
	}
	for name, value := range map[string]string{
		"id":                record.ID,
		"source_cluster_id": record.SourceClusterID,
		"source_generation": record.SourceGeneration,
		"restore_plan_id":   record.RestorePlanID,
		"checkpoint_id":     record.CheckpointID,
		"target_cluster_id": record.TargetClusterID,
		"target_generation": record.TargetGeneration,
	} {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > 256 {
			return fmt.Errorf("%w: source fence %s is invalid", ErrInvalidObject, name)
		}
	}
	if record.SourceClusterID == record.TargetClusterID ||
		record.SourceGeneration == record.TargetGeneration {
		return fmt.Errorf("%w: source fence generations are not isolated", ErrInvalidObject)
	}
	if err := validateSHA256(record.CheckpointSHA256); err != nil {
		return fmt.Errorf("%w: source fence manifest digest is invalid", ErrInvalidObject)
	}
	if record.FenceControllerRevision == 0 ||
		record.RequestedAtUnixMillis <= 0 ||
		record.ConvergedAtUnixMillis < 0 ||
		(record.ConvergedAtUnixMillis > 0 &&
			record.ConvergedAtUnixMillis < record.RequestedAtUnixMillis) {
		return fmt.Errorf("%w: source fence revision or timestamps are invalid", ErrInvalidObject)
	}
	if requireConverged && record.ConvergedAtUnixMillis == 0 {
		return fmt.Errorf("%w: source fence has not converged", ErrInvalidObject)
	}
	return nil
}

// SignSourceFenceReceipt signs one converged Controller fence record.
func SignSourceFenceReceipt(
	ctx context.Context,
	record SourceFenceRecord,
	signer ManifestSigner,
	signingKeyID string,
) (SourceFenceReceipt, error) {
	if signer == nil || strings.TrimSpace(signingKeyID) == "" {
		return SourceFenceReceipt{},
			fmt.Errorf("%w: source fence signer and key id are required", ErrInvalidSignature)
	}
	if err := ValidateSourceFenceRecord(record, true); err != nil {
		return SourceFenceReceipt{}, err
	}
	receipt := SourceFenceReceipt{SourceFenceRecord: record}
	canonical, err := canonicalSourceFenceReceipt(receipt)
	if err != nil {
		return SourceFenceReceipt{}, err
	}
	signature, err := signer.Sign(ctx, signingKeyID, canonical)
	if err != nil {
		return SourceFenceReceipt{},
			fmt.Errorf("%w: sign source fence receipt: %v", ErrInvalidSignature, err)
	}
	if signature.KeyID != signingKeyID ||
		strings.TrimSpace(signature.Algorithm) == "" ||
		len(signature.Value) == 0 {
		return SourceFenceReceipt{},
			fmt.Errorf("%w: source fence signer metadata mismatch", ErrInvalidSignature)
	}
	receipt.Signature = &signature
	if err := validateSourceFenceReceipt(receipt, true); err != nil {
		return SourceFenceReceipt{}, err
	}
	return receipt, nil
}

// MarshalSourceFenceReceipt serializes one validated signed receipt.
func MarshalSourceFenceReceipt(receipt SourceFenceReceipt) ([]byte, error) {
	if err := validateSourceFenceReceipt(receipt, true); err != nil {
		return nil, err
	}
	body, err := json.Marshal(receipt)
	if err != nil {
		return nil, fmt.Errorf("marshal source fence receipt: %w", err)
	}
	if len(body) > maxSourceFenceReceiptBytes {
		return nil, fmt.Errorf("%w: source fence receipt exceeds size limit", ErrInvalidObject)
	}
	return body, nil
}

// LoadSourceFenceReceipt strictly decodes and verifies one signed receipt.
func LoadSourceFenceReceipt(
	ctx context.Context,
	body []byte,
	signer ManifestSigner,
) (SourceFenceReceipt, error) {
	if signer == nil {
		return SourceFenceReceipt{},
			fmt.Errorf("%w: source fence verifier is required", ErrInvalidSignature)
	}
	if len(body) == 0 || len(body) > maxSourceFenceReceiptBytes {
		return SourceFenceReceipt{},
			fmt.Errorf("%w: source fence receipt size is outside bounds", ErrInvalidObject)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var receipt SourceFenceReceipt
	if err := decoder.Decode(&receipt); err != nil {
		return SourceFenceReceipt{},
			fmt.Errorf("%w: decode source fence receipt: %v", ErrInvalidObject, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return SourceFenceReceipt{},
			fmt.Errorf("%w: trailing source fence receipt data", ErrInvalidObject)
	}
	if err := VerifySourceFenceReceipt(ctx, receipt, signer); err != nil {
		return SourceFenceReceipt{}, err
	}
	return receipt, nil
}

// VerifySourceFenceReceipt validates and authenticates one receipt value.
func VerifySourceFenceReceipt(
	ctx context.Context,
	receipt SourceFenceReceipt,
	signer ManifestSigner,
) error {
	if signer == nil {
		return fmt.Errorf("%w: source fence verifier is required", ErrInvalidSignature)
	}
	if err := validateSourceFenceReceipt(receipt, true); err != nil {
		return err
	}
	canonical, err := canonicalSourceFenceReceipt(receipt)
	if err != nil {
		return err
	}
	if err := signer.Verify(ctx, *receipt.Signature, canonical); err != nil {
		return fmt.Errorf("%w: verify source fence receipt: %v", ErrInvalidSignature, err)
	}
	return nil
}

// SourceFenceReceiptDigest returns the lowercase SHA-256 of the complete
// validated signed receipt bytes.
func SourceFenceReceiptDigest(receipt SourceFenceReceipt) (string, error) {
	body, err := MarshalSourceFenceReceipt(receipt)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

func canonicalSourceFenceReceipt(receipt SourceFenceReceipt) ([]byte, error) {
	receipt.Signature = nil
	if err := validateSourceFenceReceipt(receipt, false); err != nil {
		return nil, err
	}
	body, err := json.Marshal(receipt)
	if err != nil {
		return nil, fmt.Errorf("canonical source fence receipt: %w", err)
	}
	return body, nil
}

func validateSourceFenceReceipt(
	receipt SourceFenceReceipt,
	requireSignature bool,
) error {
	if err := ValidateSourceFenceRecord(receipt.SourceFenceRecord, true); err != nil {
		return err
	}
	if requireSignature {
		if receipt.Signature == nil ||
			strings.TrimSpace(receipt.Signature.KeyID) == "" ||
			strings.TrimSpace(receipt.Signature.Algorithm) == "" ||
			len(receipt.Signature.Value) == 0 {
			return fmt.Errorf("%w: source fence signature is required", ErrInvalidSignature)
		}
	} else if receipt.Signature != nil {
		return fmt.Errorf("%w: unsigned source fence contains a signature", ErrInvalidObject)
	}
	return nil
}
