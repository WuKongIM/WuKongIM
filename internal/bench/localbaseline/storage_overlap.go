package localbaseline

import (
	"bufio"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	maximumStorageOverlapBytes    = 2 << 20
	maximumStorageOverlapRows     = 4096
	maximumSnapshotInventoryBytes = 512 << 10
	maximumSnapshotInventoryFiles = 4096
)

var storageOverlapHeader = []string{
	"observed_at_utc", "run_id", "sample", "node", "status", "compaction_count",
	"compactions_in_progress", "snapshot_files", "snapshot_bytes", "snapshot_identity", "snapshot_inventory",
}

// ArtifactReader returns one bounded, already authenticated sibling artifact.
// Callers that verify a checksum manifest can therefore rebuild evidence
// without reopening mutable filesystem paths.
type ArtifactReader func(relative string, maximum int64) ([]byte, error)

// ReadStorageOverlapEvidence strictly reads one retained single-node cluster
// overlap TSV and verifies every referenced snapshot inventory against its
// recorded digest, file count, and byte total.
func ReadStorageOverlapEvidence(path, expectedRunID string) (StorageOverlapEvidence, error) {
	path = filepath.Clean(path)
	directory := filepath.Dir(path)
	directoryInfo, directoryErr := os.Lstat(directory)
	if directoryErr != nil || !directoryInfo.IsDir() || directoryInfo.Mode()&os.ModeSymlink != 0 {
		return StorageOverlapEvidence{}, errors.New("storage overlap evidence directory is unavailable")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maximumStorageOverlapBytes {
		return StorageOverlapEvidence{}, errors.New("storage overlap evidence is unavailable")
	}
	file, err := os.Open(path)
	if err != nil {
		return StorageOverlapEvidence{}, err
	}
	defer file.Close()

	return ParseStorageOverlapEvidence(file, expectedRunID, func(relative string, maximum int64) ([]byte, error) {
		if !validSnapshotInventoryEntry(relative) {
			return nil, errors.New("artifact path is unsafe")
		}
		path := filepath.Join(directory, filepath.FromSlash(relative))
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maximum {
			return nil, errors.New("file is unavailable")
		}
		opened, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer opened.Close()
		body, err := io.ReadAll(io.LimitReader(opened, maximum+1))
		if err != nil {
			return nil, err
		}
		if int64(len(body)) > maximum {
			return nil, errors.New("file exceeds size limit")
		}
		return body, nil
	})
}

// ParseStorageOverlapEvidence rebuilds typed overlap evidence only from the
// supplied TSV stream and authenticated sibling inventory bytes.
func ParseStorageOverlapEvidence(reader io.Reader, expectedRunID string, readArtifact ArtifactReader) (StorageOverlapEvidence, error) {
	if reader == nil || readArtifact == nil {
		return StorageOverlapEvidence{}, errors.New("storage overlap authenticated readers are required")
	}
	body, err := io.ReadAll(io.LimitReader(reader, maximumStorageOverlapBytes+1))
	if err != nil {
		return StorageOverlapEvidence{}, err
	}
	if len(body) > maximumStorageOverlapBytes {
		return StorageOverlapEvidence{}, fmt.Errorf("storage overlap evidence exceeds %d bytes", maximumStorageOverlapBytes)
	}
	scanner := bufio.NewScanner(strings.NewReader(string(body)))
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	evidence := StorageOverlapEvidence{
		CaptureComplete: true,
		PayloadSHA256:   fmt.Sprintf("%x", sha256.Sum256(body)),
		Samples:         make([]StorageOverlapSample, 0, 16),
	}
	seen := make(map[string]struct{})
	lineNumber := 0
	var previous time.Time
	for scanner.Scan() {
		lineNumber++
		if lineNumber > maximumStorageOverlapRows+1 {
			return StorageOverlapEvidence{}, errors.New("storage overlap evidence has too many rows")
		}
		columns := strings.Split(scanner.Text(), "\t")
		if lineNumber == 1 {
			if !sameStorageColumns(columns, storageOverlapHeader) {
				return StorageOverlapEvidence{}, errors.New("storage overlap header is invalid")
			}
			continue
		}
		if len(columns) != len(storageOverlapHeader) {
			return StorageOverlapEvidence{}, fmt.Errorf("storage overlap row %d is invalid", lineNumber)
		}
		sample, rowErr := parseStorageOverlapRow(expectedRunID, columns, readArtifact)
		if rowErr != nil {
			return StorageOverlapEvidence{}, fmt.Errorf("storage overlap row %d: %w", lineNumber, rowErr)
		}
		if !previous.IsZero() && !sample.ObservedAt.After(previous) {
			return StorageOverlapEvidence{}, fmt.Errorf("storage overlap row %d is not strictly ordered", lineNumber)
		}
		previous = sample.ObservedAt
		if _, duplicate := seen[sample.Sample]; duplicate {
			return StorageOverlapEvidence{}, fmt.Errorf("storage overlap row %d duplicates sample", lineNumber)
		}
		seen[sample.Sample] = struct{}{}
		if sample.Status != "complete" {
			evidence.CaptureComplete = false
		}
		evidence.Samples = append(evidence.Samples, sample)
	}
	if err := scanner.Err(); err != nil {
		return StorageOverlapEvidence{}, err
	}
	if lineNumber == 0 || len(evidence.Samples) == 0 {
		return StorageOverlapEvidence{}, errors.New("storage overlap evidence is absent")
	}
	return evidence, nil
}

func parseStorageOverlapRow(expectedRunID string, columns []string, readArtifact ArtifactReader) (StorageOverlapSample, error) {
	at, err := time.Parse(time.RFC3339Nano, columns[0])
	_, offset := at.Zone()
	if err != nil || offset != 0 {
		return StorageOverlapSample{}, errors.New("observed_at_utc is invalid")
	}
	if strings.TrimSpace(expectedRunID) == "" || columns[1] != expectedRunID || !validStorageSampleName(columns[2]) || columns[3] != "node-1" {
		return StorageOverlapSample{}, errors.New("identity is invalid")
	}
	sample := StorageOverlapSample{
		ObservedAt: at.UTC(), RunID: columns[1], Sample: columns[2], Node: columns[3], Status: columns[4],
	}
	if sample.Status == "missing" {
		for _, value := range columns[5:] {
			if value != "unavailable" {
				return StorageOverlapSample{}, errors.New("missing values are invalid")
			}
		}
		return sample, nil
	}
	if sample.Status != "complete" {
		return StorageOverlapSample{}, errors.New("status is invalid")
	}
	values := []*uint64{&sample.CompactionCount, &sample.CompactionsInProgress, &sample.SnapshotFiles, &sample.SnapshotBytes}
	for index, destination := range values {
		value, parseErr := strconv.ParseUint(columns[index+5], 10, 64)
		if parseErr != nil {
			return StorageOverlapSample{}, errors.New("numeric value is invalid")
		}
		*destination = value
	}
	sample.SnapshotIdentity = columns[9]
	sample.SnapshotInventory = columns[10]
	if !validSnapshotIdentity(sample.SnapshotIdentity) {
		return StorageOverlapSample{}, errors.New("snapshot identity is invalid")
	}
	wantInventory := "snapshot-inventory/" + sample.Sample + "-node-1.tsv"
	if sample.SnapshotInventory != wantInventory {
		return StorageOverlapSample{}, errors.New("snapshot inventory path is invalid")
	}
	body, err := readArtifact(sample.SnapshotInventory, maximumSnapshotInventoryBytes)
	if err != nil {
		return StorageOverlapSample{}, fmt.Errorf("snapshot inventory: %w", err)
	}
	if err := verifySnapshotInventoryBytes(body, sample); err != nil {
		return StorageOverlapSample{}, fmt.Errorf("snapshot inventory: %w", err)
	}
	sample.InventoryVerified = true
	return sample, nil
}

func verifySnapshotInventoryBytes(body []byte, sample StorageOverlapSample) error {
	if len(body) > maximumSnapshotInventoryBytes {
		return errors.New("file exceeds size limit")
	}
	if fmt.Sprintf("%x", sha256.Sum256(body)) != sample.SnapshotIdentity {
		return errors.New("digest does not match")
	}
	if len(body) > 0 && body[len(body)-1] != '\n' {
		return errors.New("final newline is missing")
	}
	lines := strings.Split(strings.TrimSuffix(string(body), "\n"), "\n")
	if len(body) == 0 {
		lines = nil
	}
	if uint64(len(lines)) != sample.SnapshotFiles || len(lines) > maximumSnapshotInventoryFiles {
		return errors.New("file count does not match")
	}
	var totalBytes uint64
	previous := ""
	for _, line := range lines {
		columns := strings.Split(line, "\t")
		if len(columns) != 2 || !validSnapshotInventoryEntry(columns[0]) || columns[0] <= previous {
			return errors.New("entry is invalid")
		}
		size, parseErr := strconv.ParseUint(columns[1], 10, 64)
		if parseErr != nil || math.MaxUint64-totalBytes < size {
			return errors.New("byte total is invalid")
		}
		totalBytes += size
		previous = columns[0]
	}
	if totalBytes != sample.SnapshotBytes {
		return errors.New("byte total does not match")
	}
	return nil
}

func validSnapshotInventoryEntry(value string) bool {
	if value == "" || filepath.IsAbs(value) || strings.ContainsAny(value, "\t\r\n\\") {
		return false
	}
	clean := filepath.Clean(value)
	return clean == value && clean != "." && clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

func validStorageSampleName(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if character != '-' && character != '_' && (character < '0' || character > '9') &&
			(character < 'A' || character > 'Z') && (character < 'a' || character > 'z') {
			return false
		}
	}
	return true
}

func validSnapshotIdentity(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func sameStorageColumns(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
