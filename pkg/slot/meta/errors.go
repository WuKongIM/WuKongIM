package meta

import "errors"

var (
	ErrNotFound         = errors.New("metadb: not found")
	ErrAlreadyExists    = errors.New("metadb: already exists")
	ErrChecksumMismatch = errors.New("metadb: checksum mismatch")
	ErrCorruptValue     = errors.New("metadb: corrupt value")
	ErrInvalidArgument  = errors.New("metadb: invalid argument")
)
