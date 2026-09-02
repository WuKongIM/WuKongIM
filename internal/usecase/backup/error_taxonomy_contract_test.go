package backup

import (
	"errors"
	"testing"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

func TestArtifactErrorsKeepStableOperatorTaxonomy(t *testing.T) {
	t.Parallel()
	transient := errors.New("temporary repository outage")
	testCases := []struct {
		name       string
		input      error
		wantPublic error
	}{
		{name: "success", input: nil, wantPublic: nil},
		{
			name: "malformed request", input: backupartifact.ErrInvalidObject,
			wantPublic: ErrInvalidRequest,
		},
		{
			name: "missing archive", input: backupartifact.ErrObjectNotFound,
			wantPublic: ErrArchiveNotFound,
		},
		{
			name: "damaged archive", input: backupartifact.ErrObjectCorrupt,
			wantPublic: ErrArchiveCorrupt,
		},
		{name: "transient", input: transient, wantPublic: transient},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := normalizeArtifactError(testCase.input)
			if testCase.wantPublic == nil {
				if err != nil {
					t.Fatalf("normalizeArtifactError(nil) = %v", err)
				}
				return
			}
			if !errors.Is(err, testCase.wantPublic) ||
				!errors.Is(err, testCase.input) {
				t.Fatalf(
					"normalizeArtifactError(%v) = %v, want public %v and original cause",
					testCase.input, err, testCase.wantPublic,
				)
			}
		})
	}
}

func TestStoreAccessErrorsPreserveStageEvidenceAndPublicClassification(t *testing.T) {
	t.Parallel()
	transportErr := errors.New("connection reset")
	typed := &backupcontract.RepositoryAccessError{
		Provider: backupcontract.StoreKindOSS,
		Stage:    backupcontract.RepositoryAccessReadMarker,
		Reason:   backupcontract.RepositoryAccessDenied,
		Cause:    transportErr,
	}
	if err := normalizeStoreAccessError(typed); !errors.Is(err, ErrStoreUnreachable) ||
		!errors.Is(err, transportErr) {
		t.Fatalf("normalizeStoreAccessError(typed) = %v", err)
	}
	if err := normalizeStoreAccessError(
		backupartifact.ErrInvalidManifest,
	); !errors.Is(err, ErrInvalidRequest) || errors.Is(err, ErrStoreUnreachable) {
		t.Fatalf("normalizeStoreAccessError(invalid manifest) = %v", err)
	}
	if err := normalizeStoreAccessError(
		backupartifact.ErrObjectNotFound,
	); !errors.Is(err, ErrStoreUnreachable) || !errors.Is(err, ErrArchiveNotFound) {
		t.Fatalf("normalizeStoreAccessError(missing object) = %v", err)
	}
	if err := normalizeStoreAccessError(nil); err != nil {
		t.Fatalf("normalizeStoreAccessError(nil) = %v", err)
	}
}

func TestRepositoryAccessErrorFillsOnlyMissingEvidence(t *testing.T) {
	t.Parallel()
	if err := repositoryAccessError(
		backupcontract.StoreKindS3,
		backupcontract.RepositoryAccessOpen,
		backupcontract.RepositoryAccessDenied,
		nil,
	); err != nil {
		t.Fatalf("repositoryAccessError(nil) = %v", err)
	}
	cause := errors.New("permission denied")
	existing := &backupcontract.RepositoryAccessError{
		Reason: backupcontract.RepositoryAccessUnknown,
		Cause:  cause,
	}
	completed := repositoryAccessError(
		backupcontract.StoreKindCOS,
		backupcontract.RepositoryAccessWriteMarker,
		backupcontract.RepositoryAccessDenied,
		existing,
	)
	var completedAccess *backupcontract.RepositoryAccessError
	if !errors.As(completed, &completedAccess) ||
		completedAccess.Provider != backupcontract.StoreKindCOS ||
		completedAccess.Stage != backupcontract.RepositoryAccessWriteMarker ||
		completedAccess.Reason != backupcontract.RepositoryAccessDenied ||
		!errors.Is(completedAccess, cause) {
		t.Fatalf("completed access evidence = %#v", completedAccess)
	}
	if existing.Provider != "" || existing.Stage != "" ||
		existing.Reason != backupcontract.RepositoryAccessUnknown {
		t.Fatalf("input access error was mutated: %#v", existing)
	}

	preserved := &backupcontract.RepositoryAccessError{
		Provider: backupcontract.StoreKindOSS,
		Stage:    backupcontract.RepositoryAccessDelete,
		Reason:   backupcontract.RepositoryAccessDenied,
		Cause:    cause,
	}
	result := repositoryAccessError(
		backupcontract.StoreKindS3,
		backupcontract.RepositoryAccessOpen,
		backupcontract.RepositoryAccessUnknown,
		preserved,
	)
	var resultAccess *backupcontract.RepositoryAccessError
	if !errors.As(result, &resultAccess) ||
		resultAccess.Provider != preserved.Provider ||
		resultAccess.Stage != preserved.Stage ||
		resultAccess.Reason != preserved.Reason {
		t.Fatalf("preserved access evidence = %#v", resultAccess)
	}

	wrapped := repositoryAccessError(
		backupcontract.StoreKindFile,
		backupcontract.RepositoryAccessOpen,
		"",
		cause,
	)
	var wrappedAccess *backupcontract.RepositoryAccessError
	if !errors.As(wrapped, &wrappedAccess) ||
		wrappedAccess.Provider != backupcontract.StoreKindFile ||
		wrappedAccess.Stage != backupcontract.RepositoryAccessOpen ||
		wrappedAccess.Reason != backupcontract.RepositoryAccessUnknown ||
		!errors.Is(wrappedAccess, cause) {
		t.Fatalf("wrapped access evidence = %#v", wrappedAccess)
	}
}
