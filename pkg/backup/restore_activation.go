package backup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// RestoreActivationKind identifies the reviewed evidence used to open a successor.
type RestoreActivationKind string

const (
	// RestoreActivationSourceFence uses a cryptographically verified source receipt.
	RestoreActivationSourceFence RestoreActivationKind = "source_fence_receipt"
	// RestoreActivationBreakGlass records explicit recovery when the source is unrecoverable.
	RestoreActivationBreakGlass RestoreActivationKind = "break_glass"
)

// BreakGlassActivationAudit is the immutable exceptional authorization record.
type BreakGlassActivationAudit struct {
	// ID uniquely identifies the exceptional authorization.
	ID string `json:"id"`
	// RestorePlanID binds the authorization to one successor plan.
	RestorePlanID string `json:"restore_plan_id"`
	// Operator is the authenticated Manager principal.
	Operator string `json:"operator"`
	// Reason is the explicit bounded operator justification.
	Reason string `json:"reason"`
	// AuthorizedAtUnixMillis is the target Controller audit time.
	AuthorizedAtUnixMillis int64 `json:"authorized_at_unix_millis"`
}

// RestoreActivationEvidence is the immutable target Controller audit record.
type RestoreActivationEvidence struct {
	// Kind selects normal receipt verification or exceptional break-glass.
	Kind RestoreActivationKind `json:"kind"`
	// EvidenceSHA256 authenticates the complete signed receipt or break-glass audit.
	EvidenceSHA256 string `json:"evidence_sha256"`
	// Operator is the authenticated principal that requested activation.
	Operator string `json:"operator"`
	// RecordedAtUnixMillis is when the target Controller committed activation.
	RecordedAtUnixMillis int64 `json:"recorded_at_unix_millis"`
	// SourceFenceReceipt is present only on the normal path.
	SourceFenceReceipt *SourceFenceReceipt `json:"source_fence_receipt,omitempty"`
	// BreakGlass is present only on the exceptional path.
	BreakGlass *BreakGlassActivationAudit `json:"break_glass,omitempty"`
}

// BreakGlassActivationDigest validates and hashes the canonical audit record.
func BreakGlassActivationDigest(audit BreakGlassActivationAudit) (string, error) {
	if err := validateBreakGlassActivationAudit(audit); err != nil {
		return "", err
	}
	body, err := json.Marshal(audit)
	if err != nil {
		return "", fmt.Errorf("marshal break-glass activation audit: %w", err)
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

// ValidateRestoreActivationEvidence checks durable structural and digest invariants.
// Cryptographic receipt verification remains the restore use case's responsibility.
func ValidateRestoreActivationEvidence(evidence RestoreActivationEvidence) error {
	operator := strings.TrimSpace(evidence.Operator)
	if operator == "" || len(operator) > 256 ||
		evidence.RecordedAtUnixMillis <= 0 ||
		validateSHA256(evidence.EvidenceSHA256) != nil {
		return fmt.Errorf("%w: restore activation audit metadata is invalid", ErrInvalidObject)
	}
	switch evidence.Kind {
	case RestoreActivationSourceFence:
		if evidence.SourceFenceReceipt == nil || evidence.BreakGlass != nil {
			return fmt.Errorf("%w: source-fence activation evidence is incomplete", ErrInvalidObject)
		}
		if err := validateSourceFenceReceipt(*evidence.SourceFenceReceipt, true); err != nil {
			return err
		}
		digest, err := SourceFenceReceiptDigest(*evidence.SourceFenceReceipt)
		if err != nil || digest != evidence.EvidenceSHA256 {
			return fmt.Errorf("%w: source-fence activation digest mismatch", ErrInvalidObject)
		}
	case RestoreActivationBreakGlass:
		if evidence.SourceFenceReceipt != nil || evidence.BreakGlass == nil ||
			evidence.BreakGlass.Operator != evidence.Operator ||
			evidence.BreakGlass.AuthorizedAtUnixMillis !=
				evidence.RecordedAtUnixMillis {
			return fmt.Errorf("%w: break-glass activation evidence is incomplete", ErrInvalidObject)
		}
		digest, err := BreakGlassActivationDigest(*evidence.BreakGlass)
		if err != nil || digest != evidence.EvidenceSHA256 {
			return fmt.Errorf("%w: break-glass activation digest mismatch", ErrInvalidObject)
		}
	default:
		return fmt.Errorf("%w: restore activation evidence kind is invalid", ErrInvalidObject)
	}
	return nil
}

func validateBreakGlassActivationAudit(audit BreakGlassActivationAudit) error {
	if strings.TrimSpace(audit.ID) == "" || len(audit.ID) > 256 ||
		strings.TrimSpace(audit.RestorePlanID) == "" ||
		len(audit.RestorePlanID) > 256 ||
		strings.TrimSpace(audit.Operator) == "" || len(audit.Operator) > 256 ||
		audit.AuthorizedAtUnixMillis <= 0 {
		return fmt.Errorf("%w: break-glass activation audit identity is invalid", ErrInvalidObject)
	}
	reason := strings.TrimSpace(audit.Reason)
	if len(reason) < 16 || len(reason) > 1024 || !utf8.ValidString(reason) {
		return fmt.Errorf("%w: break-glass activation reason is invalid", ErrInvalidObject)
	}
	return nil
}

// CloneRestoreActivationEvidence returns a detached evidence copy.
func CloneRestoreActivationEvidence(
	evidence *RestoreActivationEvidence,
) *RestoreActivationEvidence {
	if evidence == nil {
		return nil
	}
	out := *evidence
	if evidence.SourceFenceReceipt != nil {
		receipt := *evidence.SourceFenceReceipt
		if evidence.SourceFenceReceipt.Signature != nil {
			signature := *evidence.SourceFenceReceipt.Signature
			signature.Value = append([]byte(nil), signature.Value...)
			receipt.Signature = &signature
		}
		out.SourceFenceReceipt = &receipt
	}
	if evidence.BreakGlass != nil {
		audit := *evidence.BreakGlass
		out.BreakGlass = &audit
	}
	return &out
}
