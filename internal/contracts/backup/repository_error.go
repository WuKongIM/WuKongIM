package backup

import (
	"fmt"
	"strings"
)

// RepositoryAccessReason is a stable, secret-safe repository failure class.
type RepositoryAccessReason string

const (
	RepositoryAccessInvalidAccessKey    RepositoryAccessReason = "invalid_access_key"
	RepositoryAccessSignatureMismatch   RepositoryAccessReason = "signature_mismatch"
	RepositoryAccessDenied              RepositoryAccessReason = "access_denied"
	RepositoryAccessBucketNotFound      RepositoryAccessReason = "bucket_not_found"
	RepositoryAccessRegionMismatch      RepositoryAccessReason = "region_mismatch"
	RepositoryAccessEndpointUnreachable RepositoryAccessReason = "endpoint_unreachable"
	RepositoryAccessTLSFailure          RepositoryAccessReason = "tls_failure"
	RepositoryAccessTimeout             RepositoryAccessReason = "timeout"
	RepositoryAccessReadFailed          RepositoryAccessReason = "read_failed"
	RepositoryAccessWriteFailed         RepositoryAccessReason = "write_failed"
	RepositoryAccessListFailed          RepositoryAccessReason = "list_failed"
	RepositoryAccessDeleteFailed        RepositoryAccessReason = "delete_failed"
	RepositoryAccessRepositoryInUse     RepositoryAccessReason = "repository_in_use"
	RepositoryAccessNodeUnreachable     RepositoryAccessReason = "node_unreachable"
	RepositoryAccessUnknown             RepositoryAccessReason = "unknown"
)

// RepositoryAccessStage identifies the exact test operation that failed.
type RepositoryAccessStage string

const (
	RepositoryAccessOpen         RepositoryAccessStage = "open"
	RepositoryAccessWriteMarker  RepositoryAccessStage = "write_marker"
	RepositoryAccessReadMarker   RepositoryAccessStage = "read_marker"
	RepositoryAccessWriteReceipt RepositoryAccessStage = "write_receipt"
	RepositoryAccessReadReceipt  RepositoryAccessStage = "read_receipt"
	RepositoryAccessList         RepositoryAccessStage = "list"
	RepositoryAccessDelete       RepositoryAccessStage = "delete"
	RepositoryAccessBindIdentity RepositoryAccessStage = "bind_identity"
	RepositoryAccessMarkVerified RepositoryAccessStage = "mark_verified"
)

// RepositoryAccessError retains the internal cause while exposing only
// bounded provider diagnostics that are safe for Manager and node RPC.
type RepositoryAccessError struct {
	Reason       RepositoryAccessReason
	Stage        RepositoryAccessStage
	Provider     StoreKind
	ProviderCode string
	RequestID    string
	NodeID       uint64
	Cause        error `json:"-"`
}

// Error returns a secret-safe diagnostic and never formats Cause.
func (e *RepositoryAccessError) Error() string {
	if e == nil {
		return "backup repository access failed"
	}
	message := fmt.Sprintf(
		"backup repository access failed: provider=%s stage=%s reason=%s",
		safeRepositoryDiagnostic(string(e.Provider)),
		safeRepositoryDiagnostic(string(e.Stage)),
		safeRepositoryDiagnostic(string(e.Reason)),
	)
	if code := safeRepositoryDiagnostic(e.ProviderCode); code != "" {
		message += " provider_code=" + code
	}
	if requestID := safeRepositoryDiagnostic(e.RequestID); requestID != "" {
		message += " request_id=" + requestID
	}
	if e.NodeID != 0 {
		message += fmt.Sprintf(" node=%d", e.NodeID)
	}
	return message
}

// Unwrap retains internal matching and diagnostics without exposing the cause
// through Error or JSON.
func (e *RepositoryAccessError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func safeRepositoryDiagnostic(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 256 {
		value = value[:256]
	}
	return strings.Map(func(char rune) rune {
		if char < 0x20 || char == 0x7f {
			return -1
		}
		return char
	}, value)
}
