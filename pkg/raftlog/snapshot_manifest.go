package raftlog

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"math"
	"path/filepath"
	"regexp"
	"strings"

	"go.etcd.io/raft/v3/raftpb"
)

const (
	snapshotManifestVersion uint16 = 1
	snapshotChecksumCRC32C         = "crc32c"
)

var snapshotIDPattern = regexp.MustCompile(`^snap-[0-9a-f]{16}-[0-9a-f]{16}-[0-9a-f]{16}$`)
var snapshotCRC32CTable = crc32.MakeTable(crc32.Castagnoli)

// SnapshotManifest describes externally chunked Raft snapshot payload bytes.
type SnapshotManifest struct {
	// Version identifies the binary manifest schema.
	Version uint16
	// ScopeKind is the durable raftlog scope kind that owns this snapshot.
	ScopeKind uint8
	// ScopeID is the durable raftlog scope ID that owns this snapshot.
	ScopeID uint64
	// Index is the Raft snapshot index.
	Index uint64
	// Term is the Raft snapshot term.
	Term uint64
	// ConfState is the Raft configuration state at the snapshot index.
	ConfState raftpb.ConfState
	// SnapshotID is the basename of the external snapshot chunk directory.
	SnapshotID string
	// ChunkSize is the maximum payload bytes stored in each chunk file.
	ChunkSize uint64
	// ChunkCount is the exact number of external chunk files.
	ChunkCount uint32
	// TotalSize is the total snapshot payload size in bytes.
	TotalSize uint64
	// ChecksumType names the checksum algorithm used for all checksum fields.
	ChecksumType string
	// WholeChecksum is the checksum of the assembled snapshot payload.
	WholeChecksum []byte
	// ChunkChecksums contains one checksum for each chunk, in chunk order.
	ChunkChecksums [][]byte
	// CreatedAtUnixNano records when this manifest was generated.
	CreatedAtUnixNano int64
}

// Validate checks that the manifest is well-formed and owned by scope.
func (m SnapshotManifest) Validate(scope Scope) error {
	if m.Version != snapshotManifestVersion {
		return errors.New("raftstorage: invalid snapshot manifest version")
	}
	if !isValidScopeKind(ScopeKind(m.ScopeKind)) || !isValidScopeKind(scope.Kind) {
		return errors.New("raftstorage: invalid snapshot manifest scope kind")
	}
	if ScopeKind(m.ScopeKind) != scope.Kind || m.ScopeID != scope.ID {
		return errors.New("raftstorage: snapshot manifest scope mismatch")
	}
	if m.Index == 0 {
		return errors.New("raftstorage: snapshot manifest index must be non-zero")
	}
	if m.Term == 0 {
		return errors.New("raftstorage: snapshot manifest term must be non-zero")
	}
	if err := validateSnapshotID(m.SnapshotID); err != nil {
		return err
	}
	if m.ChunkSize == 0 {
		return errors.New("raftstorage: snapshot manifest chunk size must be non-zero")
	}
	if m.TotalSize > uint64(maxInt()) {
		return errors.New("raftstorage: snapshot manifest total size exceeds phase-one limit")
	}
	chunkCount, err := manifestChunkCount(m.TotalSize, m.ChunkSize)
	if err != nil {
		return err
	}
	if m.ChunkCount != chunkCount {
		return errors.New("raftstorage: snapshot manifest chunk count mismatch")
	}
	if m.ChecksumType != snapshotChecksumCRC32C {
		return errors.New("raftstorage: unsupported snapshot checksum type")
	}
	if len(m.WholeChecksum) != 4 {
		return errors.New("raftstorage: invalid whole snapshot checksum length")
	}
	if m.TotalSize == 0 && !bytes.Equal(m.WholeChecksum, snapshotChecksum(nil)) {
		return errors.New("raftstorage: invalid empty snapshot checksum")
	}
	if len(m.ChunkChecksums) != int(m.ChunkCount) {
		return errors.New("raftstorage: invalid snapshot chunk checksum count")
	}
	for _, checksum := range m.ChunkChecksums {
		if len(checksum) != 4 {
			return errors.New("raftstorage: invalid snapshot chunk checksum length")
		}
	}
	return nil
}

func isValidScopeKind(kind ScopeKind) bool {
	switch kind {
	case ScopeSlot, ScopeController:
		return true
	default:
		return false
	}
}

func validateSnapshotID(snapshotID string) error {
	if snapshotID == "" || snapshotID == "." || snapshotID == ".." {
		return errors.New("raftstorage: invalid snapshot ID")
	}
	if filepath.IsAbs(snapshotID) || filepath.Base(snapshotID) != snapshotID {
		return errors.New("raftstorage: invalid snapshot ID path")
	}
	if strings.Contains(snapshotID, "/") || strings.Contains(snapshotID, `\`) {
		return errors.New("raftstorage: invalid snapshot ID separator")
	}
	if !snapshotIDPattern.MatchString(snapshotID) {
		return errors.New("raftstorage: invalid snapshot ID format")
	}
	return nil
}

func manifestChunkCount(totalSize, chunkSize uint64) (uint32, error) {
	if chunkSize == 0 {
		return 0, errors.New("raftstorage: snapshot manifest chunk size must be non-zero")
	}
	if totalSize == 0 {
		return 0, nil
	}
	count := 1 + (totalSize-1)/chunkSize
	if count > math.MaxUint32 {
		return 0, errors.New("raftstorage: snapshot manifest chunk count overflows uint32")
	}
	return uint32(count), nil
}

func snapshotChecksum(data []byte) []byte {
	sum := crc32.Checksum(data, snapshotCRC32CTable)
	checksum := make([]byte, 4)
	binary.BigEndian.PutUint32(checksum, sum)
	return checksum
}

func maxInt() int {
	return int(^uint(0) >> 1)
}
