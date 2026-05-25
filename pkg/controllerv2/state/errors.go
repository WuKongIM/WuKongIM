package state

import "errors"

var (
	// ErrInvalidState indicates that a cluster state violates durable invariants.
	ErrInvalidState = errors.New("controllerv2/state: invalid state")
	// ErrChecksumMismatch indicates that a state file checksum does not match its payload.
	ErrChecksumMismatch = errors.New("controllerv2/state: checksum mismatch")
	// ErrUnsupportedSchema indicates that a state file uses an unsupported schema version.
	ErrUnsupportedSchema = errors.New("controllerv2/state: unsupported schema")
)
