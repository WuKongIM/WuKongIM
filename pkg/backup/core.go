package backup

import (
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"strings"
)

var (
	// ErrInvalidManifest reports a structurally invalid archive manifest.
	ErrInvalidManifest = errors.New("backup: invalid manifest")
	// ErrUnsupportedVersion reports an archive version this binary cannot read.
	ErrUnsupportedVersion = errors.New("backup: unsupported version")
	// ErrInvalidObject reports invalid repository input or object metadata.
	ErrInvalidObject = errors.New("backup: invalid object")
	// ErrObjectCorrupt reports stored bytes that fail integrity validation.
	ErrObjectCorrupt = errors.New("backup: corrupt object")
	// ErrObjectExists reports an immutable repository key that already exists.
	ErrObjectExists = errors.New("backup: repository object exists")
	// ErrObjectNotFound reports a repository key that does not exist.
	ErrObjectNotFound = errors.New("backup: repository object not found")
	// ErrRepositoryIncomplete reports a repository marker or archive missing required objects.
	ErrRepositoryIncomplete = errors.New("backup: repository incomplete")
)

// Compression identifies the compression applied to archive chunks.
type Compression string

const (
	// CompressionZstd selects Zstandard compression.
	CompressionZstd Compression = "zstd"
)

func validateBackupIdentity(value string) error {
	if len(value) == 0 || len(value) > 128 {
		return fmt.Errorf("identity length is invalid")
	}
	for index, char := range value {
		if (char >= 'a' && char <= 'z') ||
			(char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') ||
			char == '-' || char == '_' || (char == '.' && index > 0) {
			continue
		}
		return fmt.Errorf("identity %q contains unsafe characters", value)
	}
	if strings.Contains(value, "..") {
		return fmt.Errorf("identity %q contains an unsafe segment", value)
	}
	return nil
}

func validateRepositoryKey(key string) error {
	if key == "" || strings.Contains(key, "\\") || strings.HasPrefix(key, "/") {
		return fmt.Errorf("unsafe key %q", key)
	}
	clean := path.Clean(key)
	if clean != key || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("unsafe key %q", key)
	}
	return nil
}

func validateSHA256(value string) error {
	if len(value) != 64 || value != strings.ToLower(value) {
		return fmt.Errorf("must be 64 lowercase hexadecimal characters")
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != 32 {
		return fmt.Errorf("must be a SHA-256 hexadecimal value")
	}
	return nil
}
