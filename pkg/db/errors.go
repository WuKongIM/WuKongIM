package db

import "github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"

var (
	// ErrInvalidArgument reports invalid caller input.
	ErrInvalidArgument = dberrors.ErrInvalidArgument
	// ErrNotFound reports that a requested key or row does not exist.
	ErrNotFound = dberrors.ErrNotFound
	// ErrAlreadyExists reports create-only writes that would overwrite existing state.
	ErrAlreadyExists = dberrors.ErrAlreadyExists
	// ErrConflict reports guard, version, or uniqueness conflicts.
	ErrConflict = dberrors.ErrConflict
	// ErrCorruptValue reports a malformed encoded value.
	ErrCorruptValue = dberrors.ErrCorruptValue
	// ErrCorruptState reports inconsistent durable state across keys or indexes.
	ErrCorruptState = dberrors.ErrCorruptState
	// ErrChecksumMismatch reports a value envelope checksum mismatch.
	ErrChecksumMismatch = dberrors.ErrChecksumMismatch
	// ErrClosed reports operations attempted after the store has closed.
	ErrClosed = dberrors.ErrClosed
)
