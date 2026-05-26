package dberrors

import "errors"

var (
	// ErrInvalidArgument reports invalid caller input.
	ErrInvalidArgument = errors.New("db: invalid argument")
	// ErrNotFound reports that a requested key or row does not exist.
	ErrNotFound = errors.New("db: not found")
	// ErrAlreadyExists reports create-only writes that would overwrite existing state.
	ErrAlreadyExists = errors.New("db: already exists")
	// ErrConflict reports guard, version, or uniqueness conflicts.
	ErrConflict = errors.New("db: conflict")
	// ErrCorruptValue reports a malformed encoded value.
	ErrCorruptValue = errors.New("db: corrupt value")
	// ErrCorruptState reports inconsistent durable state across keys or indexes.
	ErrCorruptState = errors.New("db: corrupt state")
	// ErrChecksumMismatch reports a value envelope checksum mismatch.
	ErrChecksumMismatch = errors.New("db: checksum mismatch")
	// ErrClosed reports operations attempted after the store has closed.
	ErrClosed = errors.New("db: closed")
)
