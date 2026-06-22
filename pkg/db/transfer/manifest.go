package transfer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const manifestFileName = "manifest.json"

// LoadManifest reads, validates, and verifies the checksums for a bundle manifest.
func LoadManifest(root string) (Manifest, error) {
	file, err := os.Open(filepath.Join(root, manifestFileName))
	if err != nil {
		return Manifest{}, fmt.Errorf("%w: open manifest: %v", ErrInvalidBundle, err)
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()

	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("%w: decode manifest: %v", ErrInvalidBundle, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Manifest{}, fmt.Errorf("%w: decode manifest: extra JSON data", ErrInvalidBundle)
	}
	if err := validateManifest(root, manifest); err != nil {
		return Manifest{}, fmt.Errorf("%w: %w", ErrInvalidBundle, err)
	}
	return manifest, nil
}

func validateManifest(root string, manifest Manifest) error {
	if manifest.Format != bundleFormat {
		return fmt.Errorf("%w: format %q", ErrValidation, manifest.Format)
	}
	if manifest.Version != bundleVersion {
		return fmt.Errorf("%w: version %d", ErrValidation, manifest.Version)
	}
	if manifest.HashSlotCount <= 0 {
		return fmt.Errorf("%w: hash_slot_count must be nonzero", ErrValidation)
	}
	for i, entry := range manifest.Files {
		if err := validateFileEntry(root, i, entry); err != nil {
			return err
		}
	}
	return nil
}

func validateFileEntry(root string, index int, entry FileEntry) error {
	rel, err := safeBundlePath(entry.Path)
	if err != nil {
		return fmt.Errorf("%w: file[%d] unsafe path %q: %v", ErrValidation, index, entry.Path, err)
	}
	if !knownFileKind(entry.Kind) {
		return fmt.Errorf("%w: file[%d] unknown kind %q", ErrValidation, index, entry.Kind)
	}
	if entry.Rows < 0 {
		return fmt.Errorf("%w: file[%d] rows must be nonnegative", ErrValidation, index)
	}
	if len(entry.SHA256) != sha256.Size*2 {
		return fmt.Errorf("%w: file[%d] invalid sha256 length", ErrValidation, index)
	}
	if _, err := hex.DecodeString(entry.SHA256); err != nil {
		return fmt.Errorf("%w: file[%d] invalid sha256 hex: %v", ErrValidation, index, err)
	}

	actual, err := fileSHA256(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return fmt.Errorf("%w: file[%d] checksum read %q: %v", ErrValidation, index, entry.Path, err)
	}
	if !strings.EqualFold(actual, entry.SHA256) {
		return fmt.Errorf("%w: file[%d] checksum mismatch for %q", ErrValidation, index, entry.Path)
	}
	return nil
}

func knownFileKind(kind FileKind) bool {
	switch kind {
	case FileKindMetaUsers,
		FileKindMetaDevices,
		FileKindMetaChannels,
		FileKindMetaSubscribers,
		FileKindMetaUserChannelMemberships,
		FileKindMetaConversations,
		FileKindMetaChannelLatest,
		FileKindMessageChannels,
		FileKindMessageMessages:
		return true
	default:
		return false
	}
}

func safeBundlePath(raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("empty path")
	}
	if path.IsAbs(raw) || filepath.IsAbs(raw) {
		return "", fmt.Errorf("absolute path")
	}
	if strings.Contains(raw, "\\") {
		return "", fmt.Errorf("backslash path separator")
	}

	clean := path.Clean(raw)
	if clean != raw {
		return "", fmt.Errorf("path is not clean")
	}
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("path escapes bundle root")
	}
	return clean, nil
}

func fileSHA256(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
