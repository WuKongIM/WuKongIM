package backup

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"strings"
	"syscall"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	"github.com/minio/minio-go/v7"
)

func classifyRepositoryError(
	provider backupcontract.StoreKind,
	stage backupcontract.RepositoryAccessStage,
	err error,
) error {
	if err == nil {
		return nil
	}
	var existing *backupcontract.RepositoryAccessError
	if errors.As(err, &existing) {
		clone := *existing
		if clone.Provider == "" {
			clone.Provider = provider
		}
		if clone.Stage == "" {
			clone.Stage = stage
		}
		return &clone
	}
	reason := classifyRepositoryReason(stage, err)
	accessErr := &backupcontract.RepositoryAccessError{
		Reason: reason, Stage: stage, Provider: provider, Cause: err,
	}
	var response minio.ErrorResponse
	if errors.As(err, &response) {
		accessErr.ProviderCode = boundedRepositoryErrorField(response.Code)
		accessErr.RequestID = boundedRepositoryErrorField(response.RequestID)
	}
	return accessErr
}

func classifyRepositoryReason(
	stage backupcontract.RepositoryAccessStage,
	err error,
) backupcontract.RepositoryAccessReason {
	var response minio.ErrorResponse
	if errors.As(err, &response) {
		switch strings.ToLower(strings.TrimSpace(response.Code)) {
		case "invalidaccesskeyid", "invalidaccesskey",
			"accesskeyidnotfound":
			return backupcontract.RepositoryAccessInvalidAccessKey
		case "signaturedoesnotmatch", "signaturemismatch",
			"invalidsignature":
			return backupcontract.RepositoryAccessSignatureMismatch
		case "accessdenied", "forbidden", "unauthorized":
			return backupcontract.RepositoryAccessDenied
		case "nosuchbucket", "bucketnotfound":
			return backupcontract.RepositoryAccessBucketNotFound
		case "authorizationheadermalformed", "incorrectendpoint",
			"permanentredirect", "invalidregion":
			return backupcontract.RepositoryAccessRegionMismatch
		case "requesttimeout", "requesttimedout":
			return backupcontract.RepositoryAccessTimeout
		}
		switch response.StatusCode {
		case 401, 403:
			return backupcontract.RepositoryAccessDenied
		case 301, 307:
			return backupcontract.RepositoryAccessRegionMismatch
		case 408, 504:
			return backupcontract.RepositoryAccessTimeout
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return backupcontract.RepositoryAccessTimeout
	}
	if repositoryTLSError(err) {
		return backupcontract.RepositoryAccessTLSFailure
	}
	var dnsError *net.DNSError
	if errors.As(err, &dnsError) {
		if dnsError.Timeout() {
			return backupcontract.RepositoryAccessTimeout
		}
		return backupcontract.RepositoryAccessEndpointUnreachable
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return backupcontract.RepositoryAccessTimeout
	}
	if errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ENETUNREACH) ||
		errors.Is(err, syscall.EHOSTUNREACH) ||
		errors.Is(err, syscall.ECONNRESET) {
		return backupcontract.RepositoryAccessEndpointUnreachable
	}
	switch stage {
	case backupcontract.RepositoryAccessReadMarker,
		backupcontract.RepositoryAccessReadReceipt:
		return backupcontract.RepositoryAccessReadFailed
	case backupcontract.RepositoryAccessWriteMarker,
		backupcontract.RepositoryAccessWriteReceipt:
		return backupcontract.RepositoryAccessWriteFailed
	case backupcontract.RepositoryAccessList:
		return backupcontract.RepositoryAccessListFailed
	case backupcontract.RepositoryAccessDelete:
		return backupcontract.RepositoryAccessDeleteFailed
	default:
		return backupcontract.RepositoryAccessUnknown
	}
}

func repositoryTLSError(err error) bool {
	var unknownAuthority x509.UnknownAuthorityError
	if errors.As(err, &unknownAuthority) {
		return true
	}
	var certificateInvalid x509.CertificateInvalidError
	if errors.As(err, &certificateInvalid) {
		return true
	}
	var hostnameError x509.HostnameError
	if errors.As(err, &hostnameError) {
		return true
	}
	var recordHeader tls.RecordHeaderError
	return errors.As(err, &recordHeader)
}

func boundedRepositoryErrorField(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 256 {
		return value[:256]
	}
	return value
}
