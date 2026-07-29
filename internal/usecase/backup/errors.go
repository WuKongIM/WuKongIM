package backup

import (
	"errors"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

var (
	// ErrInvalidRequest reports malformed or unsafe operator input.
	ErrInvalidRequest = errors.New("backup usecase: invalid request")
	// ErrDisabled reports an operation that requires a configured plan.
	ErrDisabled = errors.New("backup usecase: backup is not configured")
	// ErrStateConflict reports a stale Controller revision or job fence.
	ErrStateConflict = errors.New("backup usecase: state conflict")
	// ErrArchiveHeld prevents deleting an operator-protected archive.
	ErrArchiveHeld = errors.New("backup usecase: archive is held")
	// ErrArchiveInUse prevents deleting the source of an active restore.
	ErrArchiveInUse = errors.New("backup usecase: archive is in use")
	// ErrLastUsableArchive preserves at least one healthy recovery point.
	ErrLastUsableArchive = errors.New("backup usecase: cannot delete the last usable archive")
	// ErrArchiveNotFound reports an unknown published archive.
	ErrArchiveNotFound = errors.New("backup usecase: archive not found")
	// ErrArchiveCorrupt reports an archive that cannot pass integrity checks.
	ErrArchiveCorrupt = errors.New("backup usecase: archive is corrupt")
	// ErrArchiveOperationActive reports a serialized repository operation in progress.
	ErrArchiveOperationActive = errors.New("backup usecase: archive operation is active")
	// ErrStoreUnreachable reports a repository that cannot satisfy required access.
	ErrStoreUnreachable = errors.New("backup usecase: backup store is unreachable")
	// ErrRepositoryUnverified blocks backup admission for an untested repository.
	ErrRepositoryUnverified = errors.New("backup usecase: repository is not verified")
)

func normalizeArtifactError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, backupartifact.ErrInvalidObject),
		errors.Is(err, backupartifact.ErrInvalidManifest):
		return errors.Join(ErrInvalidRequest, err)
	case errors.Is(err, backupartifact.ErrObjectNotFound):
		return errors.Join(ErrArchiveNotFound, err)
	case errors.Is(err, backupartifact.ErrObjectCorrupt),
		errors.Is(err, backupartifact.ErrUnsupportedVersion),
		errors.Is(err, backupartifact.ErrRepositoryIncomplete):
		return errors.Join(ErrArchiveCorrupt, err)
	default:
		return err
	}
}

func normalizeStoreAccessError(err error) error {
	var accessErr *backupcontract.RepositoryAccessError
	if errors.As(err, &accessErr) {
		return errors.Join(ErrStoreUnreachable, err)
	}
	normalized := normalizeArtifactError(err)
	if normalized == nil || errors.Is(normalized, ErrInvalidRequest) {
		return normalized
	}
	return errors.Join(ErrStoreUnreachable, normalized)
}

func repositoryAccessError(
	provider backupcontract.StoreKind,
	stage backupcontract.RepositoryAccessStage,
	reason backupcontract.RepositoryAccessReason,
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
		if (clone.Reason == "" ||
			clone.Reason == backupcontract.RepositoryAccessUnknown) &&
			reason != "" {
			clone.Reason = reason
		}
		return &clone
	}
	if reason == "" {
		reason = backupcontract.RepositoryAccessUnknown
	}
	return &backupcontract.RepositoryAccessError{
		Provider: provider, Stage: stage, Reason: reason, Cause: err,
	}
}
