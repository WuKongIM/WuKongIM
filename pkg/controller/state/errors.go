package state

import "errors"

var (
	// ErrInvalidState indicates that a cluster state violates durable invariants.
	ErrInvalidState = errors.New("controller/state: invalid state")
	// ErrChecksumMismatch indicates that a state file checksum does not match its payload.
	ErrChecksumMismatch = errors.New("controller/state: checksum mismatch")
	// ErrUnsupportedSchema indicates that a state file uses an unsupported schema version.
	ErrUnsupportedSchema = errors.New("controller/state: unsupported schema")
)
