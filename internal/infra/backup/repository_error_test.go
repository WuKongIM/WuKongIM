package backup

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"syscall"
	"testing"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	"github.com/minio/minio-go/v7"
)

func TestClassifyRepositoryErrorMapsProviderResponses(t *testing.T) {
	testCases := []struct {
		name   string
		code   string
		status int
		reason backupcontract.RepositoryAccessReason
	}{
		{
			name:   "invalid access key",
			code:   "InvalidAccessKeyId",
			status: 403,
			reason: backupcontract.RepositoryAccessInvalidAccessKey,
		},
		{
			name:   "signature mismatch",
			code:   "SignatureDoesNotMatch",
			status: 403,
			reason: backupcontract.RepositoryAccessSignatureMismatch,
		},
		{
			name:   "permission denied",
			code:   "AccessDenied",
			status: 403,
			reason: backupcontract.RepositoryAccessDenied,
		},
		{
			name:   "bucket missing",
			code:   "NoSuchBucket",
			status: 404,
			reason: backupcontract.RepositoryAccessBucketNotFound,
		},
		{
			name:   "wrong region",
			code:   "AuthorizationHeaderMalformed",
			status: 400,
			reason: backupcontract.RepositoryAccessRegionMismatch,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			cause := minio.ErrorResponse{
				Code: testCase.code, StatusCode: testCase.status,
				RequestID: "request-1",
				Message:   "provider response body must not be forwarded",
			}

			err := classifyRepositoryError(
				backupcontract.StoreKindOSS,
				backupcontract.RepositoryAccessWriteMarker,
				fmt.Errorf("wrapped: %w", cause),
			)

			var accessErr *backupcontract.RepositoryAccessError
			if !errors.As(err, &accessErr) {
				t.Fatalf("error = %T %v", err, err)
			}
			if accessErr.Reason != testCase.reason ||
				accessErr.ProviderCode != testCase.code ||
				accessErr.RequestID != "request-1" ||
				accessErr.Stage !=
					backupcontract.RepositoryAccessWriteMarker {
				t.Fatalf("classified error = %#v", accessErr)
			}
			if !errors.As(err, new(minio.ErrorResponse)) {
				t.Fatal("classified error did not retain provider cause")
			}
		})
	}
}

func TestClassifyRepositoryErrorMapsNetworkFailures(t *testing.T) {
	testCases := []struct {
		name   string
		err    error
		reason backupcontract.RepositoryAccessReason
	}{
		{
			name:   "deadline",
			err:    context.DeadlineExceeded,
			reason: backupcontract.RepositoryAccessTimeout,
		},
		{
			name: "DNS",
			err: &net.DNSError{
				Err: "no such host", Name: "oss.invalid",
			},
			reason: backupcontract.RepositoryAccessEndpointUnreachable,
		},
		{
			name:   "connection refused",
			err:    syscall.ECONNREFUSED,
			reason: backupcontract.RepositoryAccessEndpointUnreachable,
		},
		{
			name:   "TLS certificate",
			err:    x509.UnknownAuthorityError{},
			reason: backupcontract.RepositoryAccessTLSFailure,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := classifyRepositoryError(
				backupcontract.StoreKindCOS,
				backupcontract.RepositoryAccessOpen,
				testCase.err,
			)
			var accessErr *backupcontract.RepositoryAccessError
			if !errors.As(err, &accessErr) {
				t.Fatalf("error = %T %v", err, err)
			}
			if accessErr.Reason != testCase.reason ||
				accessErr.Provider != backupcontract.StoreKindCOS ||
				accessErr.Stage != backupcontract.RepositoryAccessOpen {
				t.Fatalf("classified error = %#v", accessErr)
			}
			if !errors.Is(err, testCase.err) {
				t.Fatal("classified error did not retain network cause")
			}
		})
	}
}

func TestClassifyRepositoryErrorUsesOperationFallback(t *testing.T) {
	testCases := []struct {
		stage  backupcontract.RepositoryAccessStage
		reason backupcontract.RepositoryAccessReason
	}{
		{
			stage:  backupcontract.RepositoryAccessReadMarker,
			reason: backupcontract.RepositoryAccessReadFailed,
		},
		{
			stage:  backupcontract.RepositoryAccessWriteReceipt,
			reason: backupcontract.RepositoryAccessWriteFailed,
		},
		{
			stage:  backupcontract.RepositoryAccessList,
			reason: backupcontract.RepositoryAccessListFailed,
		},
		{
			stage:  backupcontract.RepositoryAccessDelete,
			reason: backupcontract.RepositoryAccessDeleteFailed,
		},
	}
	for _, testCase := range testCases {
		t.Run(string(testCase.stage), func(t *testing.T) {
			err := classifyRepositoryError(
				backupcontract.StoreKindS3,
				testCase.stage,
				errors.New("opaque failure"),
			)
			var accessErr *backupcontract.RepositoryAccessError
			if !errors.As(err, &accessErr) ||
				accessErr.Reason != testCase.reason {
				t.Fatalf("classified error = %#v", accessErr)
			}
		})
	}
}
