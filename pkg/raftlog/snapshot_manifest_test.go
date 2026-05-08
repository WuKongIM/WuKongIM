package raftlog

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"math"
	"reflect"
	"strings"
	"testing"

	"go.etcd.io/raft/v3/raftpb"
)

func TestSnapshotManifestRoundTrip(t *testing.T) {
	scope := SlotScope(9)
	manifest := SnapshotManifest{
		Version:           snapshotManifestVersion,
		ScopeKind:         uint8(scope.Kind),
		ScopeID:           scope.ID,
		Index:             0x1234,
		Term:              7,
		ConfState:         raftpb.ConfState{Voters: []uint64{1, 2}, Learners: []uint64{3}},
		SnapshotID:        "snap-0000000000001234-0000000000000007-0123456789abcdef",
		ChunkSize:         5,
		ChunkCount:        3,
		TotalSize:         13,
		ChecksumType:      snapshotChecksumCRC32C,
		WholeChecksum:     snapshotChecksum([]byte("hello, world!")),
		ChunkChecksums:    [][]byte{snapshotChecksum([]byte("hello")), snapshotChecksum([]byte(", wor")), snapshotChecksum([]byte("ld!"))},
		CreatedAtUnixNano: 1710000000123456789,
	}

	encoded, err := encodeSnapshotManifest(manifest)
	if err != nil {
		t.Fatalf("encodeSnapshotManifest() error = %v", err)
	}
	decoded, err := decodeSnapshotManifest(scope, encoded)
	if err != nil {
		t.Fatalf("decodeSnapshotManifest() error = %v", err)
	}

	if !reflect.DeepEqual(decoded, manifest) {
		t.Fatalf("decoded manifest = %#v, want %#v", decoded, manifest)
	}
}

func TestSnapshotManifestRejectsInvalidSnapshotID(t *testing.T) {
	cases := []string{
		"../x",
		"/tmp/snap-0000000000001234-0000000000000007-0123456789abcdef",
		"snap-0000000000001234-0000000000000007-0123456789abcdeF",
		"snap-0000000000001234-0000000000000007-0123456789abcde",
		"snap-0000000000001234/0000000000000007-0123456789abcdef",
	}
	for _, snapshotID := range cases {
		t.Run(snapshotID, func(t *testing.T) {
			manifest := validSnapshotManifest(SlotScope(9))
			manifest.SnapshotID = snapshotID
			if err := manifest.Validate(SlotScope(9)); err == nil {
				t.Fatalf("Validate() error = nil, want invalid snapshot ID error")
			}
		})
	}
}

func TestSnapshotManifestRejectsMalformedShapeAndOverflowingSizes(t *testing.T) {
	scope := SlotScope(9)
	cases := []struct {
		name string
		edit func(*SnapshotManifest)
	}{
		{name: "bad checksum count", edit: func(m *SnapshotManifest) { m.ChunkChecksums = m.ChunkChecksums[:1] }},
		{name: "bad whole checksum length", edit: func(m *SnapshotManifest) { m.WholeChecksum = []byte{1, 2, 3} }},
		{name: "bad chunk checksum length", edit: func(m *SnapshotManifest) { m.ChunkChecksums[0] = []byte{1, 2, 3} }},
		{name: "zero chunk size", edit: func(m *SnapshotManifest) { m.ChunkSize = 0 }},
		{name: "chunk count mismatch", edit: func(m *SnapshotManifest) { m.ChunkCount = 2 }},
		{name: "computed count exceeds uint32", edit: func(m *SnapshotManifest) {
			m.ChunkSize = 1
			m.TotalSize = uint64(math.MaxUint32) + 1
			m.ChunkCount = 0
			m.ChunkChecksums = nil
		}},
		{name: "total size exceeds int", edit: func(m *SnapshotManifest) {
			m.TotalSize = uint64(maxIntForTest()) + 1
			m.ChunkCount = 0
			m.ChunkChecksums = nil
		}},
		{name: "zero index", edit: func(m *SnapshotManifest) { m.Index = 0 }},
		{name: "zero term", edit: func(m *SnapshotManifest) { m.Term = 0 }},
		{name: "wrong scope", edit: func(m *SnapshotManifest) { m.ScopeID = 10 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			manifest := validSnapshotManifest(scope)
			tc.edit(&manifest)
			if err := manifest.Validate(scope); err == nil {
				t.Fatalf("Validate() error = nil, want malformed manifest error")
			}
		})
	}
}

func TestSnapshotManifestValidatesEmptyPayloadChecksum(t *testing.T) {
	scope := SlotScope(9)
	manifest := validSnapshotManifest(scope)
	manifest.TotalSize = 0
	manifest.ChunkCount = 0
	manifest.ChunkChecksums = nil
	manifest.WholeChecksum = snapshotChecksum(nil)

	if err := manifest.Validate(scope); err != nil {
		t.Fatalf("Validate() error = %v, want nil", err)
	}
}

func TestSnapshotManifestRejectsInvalidEmptyPayloadChecksum(t *testing.T) {
	scope := SlotScope(9)
	manifest := validSnapshotManifest(scope)
	manifest.TotalSize = 0
	manifest.ChunkCount = 0
	manifest.ChunkChecksums = nil
	manifest.WholeChecksum = []byte{0xde, 0xad, 0xbe, 0xef}

	if err := manifest.Validate(scope); err == nil {
		t.Fatalf("Validate() error = nil, want empty payload checksum error")
	}
}

func TestSnapshotManifestUsesCRC32CCastagnoliBigEndian(t *testing.T) {
	data := []byte("snapshot chunk bytes")
	want := make([]byte, 4)
	binary.BigEndian.PutUint32(want, crc32.Checksum(data, crc32.MakeTable(crc32.Castagnoli)))

	if got := snapshotChecksum(data); !bytes.Equal(got, want) {
		t.Fatalf("snapshotChecksum() = %x, want %x", got, want)
	}
}

func TestSnapshotCodecRejectsTrailingAndShortData(t *testing.T) {
	manifest := validSnapshotManifest(SlotScope(9))
	encoded, err := encodeSnapshotManifest(manifest)
	if err != nil {
		t.Fatalf("encodeSnapshotManifest() error = %v", err)
	}
	if _, err := decodeSnapshotManifest(SlotScope(9), append(encoded, 0)); err == nil {
		t.Fatalf("decodeSnapshotManifest() trailing error = nil, want error")
	}
	for size := 0; size < len(encoded); size++ {
		_, err := decodeSnapshotManifest(SlotScope(9), encoded[:size])
		if err == nil {
			t.Fatalf("decodeSnapshotManifest() with %d bytes error = nil, want short data error", size)
		}
		if !strings.Contains(err.Error(), "invalid") && !strings.Contains(err.Error(), "short") {
			t.Fatalf("decodeSnapshotManifest() with %d bytes error = %v, want invalid/short data", size, err)
		}
	}
}

func validSnapshotManifest(scope Scope) SnapshotManifest {
	data := []byte("hello, world!")
	return SnapshotManifest{
		Version:           snapshotManifestVersion,
		ScopeKind:         uint8(scope.Kind),
		ScopeID:           scope.ID,
		Index:             0x1234,
		Term:              7,
		ConfState:         raftpb.ConfState{Voters: []uint64{1, 2}},
		SnapshotID:        "snap-0000000000001234-0000000000000007-0123456789abcdef",
		ChunkSize:         5,
		ChunkCount:        3,
		TotalSize:         uint64(len(data)),
		ChecksumType:      snapshotChecksumCRC32C,
		WholeChecksum:     snapshotChecksum(data),
		ChunkChecksums:    [][]byte{snapshotChecksum(data[:5]), snapshotChecksum(data[5:10]), snapshotChecksum(data[10:])},
		CreatedAtUnixNano: 1710000000123456789,
	}
}

func maxIntForTest() int {
	return int(^uint(0) >> 1)
}
