package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	localSingleNodeMaximumManifestBytes   = 8 << 20
	localSingleNodeMaximumManifestEntries = 16_384
	localSingleNodeMaximumTypedInputBytes = 64 << 20
)

type localSingleNodeVerifiedManifest struct {
	root         string
	artifactRoot localSingleNodeArtifactRoot
	digest       string
	// entries records authenticated digests, never payload bodies. Real
	// baseline logs and metrics can exceed hundreds of MiB; retaining them all
	// would make verification memory-linear in the artifact size.
	entries map[string]string
}

func verifyLocalSingleNodeChecksumManifest(rootPath, manifestPath string) (localSingleNodeVerifiedManifest, error) {
	artifactRoot, err := openLocalSingleNodeArtifactRoot(rootPath)
	if err != nil {
		return localSingleNodeVerifiedManifest{}, fmt.Errorf("payload root is not a directory")
	}
	return verifyLocalSingleNodeChecksumManifestAtRoot(artifactRoot, manifestPath)
}

func verifyLocalSingleNodeChecksumManifestAtRoot(artifactRoot localSingleNodeArtifactRoot, manifestPath string) (localSingleNodeVerifiedManifest, error) {
	root := artifactRoot.path
	manifest, err := filepath.Abs(filepath.Clean(strings.TrimSpace(manifestPath)))
	if err != nil {
		return localSingleNodeVerifiedManifest{}, fmt.Errorf("resolve payload manifest: %w", err)
	}
	manifestRelative, err := artifactRoot.relative(manifest)
	if err != nil {
		return localSingleNodeVerifiedManifest{}, fmt.Errorf("payload manifest is outside payload root")
	}
	manifestData, err := artifactRoot.read(manifestRelative, localSingleNodeMaximumManifestBytes)
	if err != nil {
		return localSingleNodeVerifiedManifest{}, fmt.Errorf("read payload manifest: %w", err)
	}

	seen, err := parseLocalSingleNodeChecksumManifest(manifestData, func(relative, expected string) error {
		actual, digestErr := artifactRoot.digest(relative, 0)
		if digestErr != nil {
			return digestErr
		}
		if actual != expected {
			return fmt.Errorf("checksum mismatch")
		}
		return nil
	})
	if err != nil {
		return localSingleNodeVerifiedManifest{}, err
	}
	digest := sha256.Sum256(manifestData)
	return localSingleNodeVerifiedManifest{
		root: root, artifactRoot: artifactRoot, digest: hex.EncodeToString(digest[:]), entries: seen,
	}, nil
}

func parseLocalSingleNodeChecksumManifest(manifestData []byte, verifyEntry func(relative, expectedDigest string) error) (map[string]string, error) {
	if verifyEntry == nil {
		return nil, fmt.Errorf("payload manifest reader is required")
	}
	seen := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(string(manifestData)))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			return nil, fmt.Errorf("payload manifest contains an empty row")
		}
		parts := strings.SplitN(line, "  ", 2)
		if len(parts) != 2 || len(parts[0]) != sha256.Size*2 {
			return nil, fmt.Errorf("payload manifest row is malformed")
		}
		expected, err := hex.DecodeString(parts[0])
		if err != nil || len(expected) != sha256.Size || parts[0] != strings.ToLower(parts[0]) {
			return nil, fmt.Errorf("payload manifest digest is malformed")
		}
		relative := parts[1]
		if !safeLocalSingleNodeRelativePath(relative) {
			return nil, fmt.Errorf("payload manifest path is unsafe")
		}
		if _, duplicate := seen[relative]; duplicate {
			return nil, fmt.Errorf("payload manifest path is duplicated")
		}
		if len(seen) >= localSingleNodeMaximumManifestEntries {
			return nil, fmt.Errorf("payload manifest has too many entries")
		}
		expectedDigest := hex.EncodeToString(expected)
		if err := verifyEntry(relative, expectedDigest); err != nil {
			return nil, fmt.Errorf("read payload manifest entry %q: %w", relative, err)
		}
		seen[relative] = expectedDigest
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan payload manifest: %w", err)
	}
	if len(seen) == 0 {
		return nil, fmt.Errorf("payload manifest is empty")
	}
	return seen, nil
}

func (manifest localSingleNodeVerifiedManifest) requireInput(path string) error {
	relative, err := manifest.artifactRoot.relative(path)
	if err != nil {
		return err
	}
	return manifest.requireRelative(relative)
}

func (manifest localSingleNodeVerifiedManifest) bytesForPath(path string) ([]byte, error) {
	relative, err := manifest.artifactRoot.relative(path)
	if err != nil {
		return nil, err
	}
	return manifest.bytesForRelative(relative)
}

func (manifest localSingleNodeVerifiedManifest) bytesForRelative(relative string) ([]byte, error) {
	return manifest.boundedBytesForRelative(relative, localSingleNodeMaximumTypedInputBytes)
}

func (manifest localSingleNodeVerifiedManifest) requireRelative(relative string) error {
	if !safeLocalSingleNodeRelativePath(relative) {
		return fmt.Errorf("input path is unsafe")
	}
	_, ok := manifest.entries[relative]
	if !ok {
		return fmt.Errorf("input is absent from payload manifest")
	}
	return nil
}

func (manifest localSingleNodeVerifiedManifest) requireDigest(relative, expected string) error {
	if err := manifest.requireRelative(relative); err != nil {
		return err
	}
	if manifest.entries[relative] != expected {
		return fmt.Errorf("nested manifest digest does not match authenticated parent manifest")
	}
	return nil
}

func (manifest localSingleNodeVerifiedManifest) verifyCurrentDigest(relative, expected string) error {
	if err := manifest.requireDigest(relative, expected); err != nil {
		return err
	}
	actual, err := manifest.artifactRoot.digest(relative, 0)
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("input checksum changed after manifest verification")
	}
	return nil
}

func (manifest localSingleNodeVerifiedManifest) boundedBytesForRelative(relative string, maximum int64) ([]byte, error) {
	if err := manifest.requireRelative(relative); err != nil {
		return nil, err
	}
	data, err := manifest.artifactRoot.read(relative, maximum)
	if err != nil {
		return nil, err
	}
	actual := sha256.Sum256(data)
	if hex.EncodeToString(actual[:]) != manifest.entries[relative] {
		return nil, fmt.Errorf("input checksum changed after manifest verification")
	}
	return data, nil
}

func safeLocalSingleNodeRelativePath(relative string) bool {
	if relative == "" || strings.Contains(relative, "\\") || filepath.IsAbs(relative) {
		return false
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(relative)))
	return clean == relative && clean != "." && clean != ".." && !strings.HasPrefix(clean, "../")
}

func requireLocalSingleNodeRegularPath(root, relative string) error {
	current := root
	parts := strings.Split(relative, "/")
	for index, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink paths are not allowed")
		}
		if index < len(parts)-1 && !info.IsDir() {
			return fmt.Errorf("parent component is not a directory")
		}
		if index == len(parts)-1 && !info.Mode().IsRegular() {
			return fmt.Errorf("entry is not a regular file")
		}
	}
	return nil
}

func readLocalSingleNodeBoundedFile(path string, maximum int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maximum {
		return nil, fmt.Errorf("file exceeds %d bytes", maximum)
	}
	return data, nil
}
