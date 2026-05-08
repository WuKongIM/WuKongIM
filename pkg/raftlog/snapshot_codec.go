package raftlog

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"

	"go.etcd.io/raft/v3/raftpb"
)

var snapshotManifestMagic = [...]byte{'W', 'K', 'S', 'M'}

// encodeSnapshotManifest serializes a snapshot manifest into the Pebble value format.
func encodeSnapshotManifest(manifest SnapshotManifest) ([]byte, error) {
	scope := Scope{Kind: ScopeKind(manifest.ScopeKind), ID: manifest.ScopeID}
	if err := manifest.Validate(scope); err != nil {
		return nil, err
	}
	confState, err := manifest.ConfState.Marshal()
	if err != nil {
		return nil, err
	}
	if len(manifest.SnapshotID) > math.MaxUint16 || len(manifest.ChecksumType) > math.MaxUint16 || len(manifest.WholeChecksum) > math.MaxUint16 {
		return nil, errors.New("raftstorage: snapshot manifest field too large")
	}
	if len(confState) > math.MaxUint32 || len(manifest.ChunkChecksums) > math.MaxUint32 {
		return nil, errors.New("raftstorage: snapshot manifest field too large")
	}
	for _, checksum := range manifest.ChunkChecksums {
		if len(checksum) > math.MaxUint16 {
			return nil, errors.New("raftstorage: snapshot manifest checksum too large")
		}
	}

	buf := make([]byte, 0, 128+len(confState)+len(manifest.SnapshotID)+len(manifest.ChecksumType))
	buf = append(buf, snapshotManifestMagic[:]...)
	buf = binary.BigEndian.AppendUint16(buf, manifest.Version)
	buf = append(buf, manifest.ScopeKind)
	buf = binary.BigEndian.AppendUint64(buf, manifest.ScopeID)
	buf = binary.BigEndian.AppendUint64(buf, manifest.Index)
	buf = binary.BigEndian.AppendUint64(buf, manifest.Term)
	buf = binary.BigEndian.AppendUint64(buf, manifest.ChunkSize)
	buf = binary.BigEndian.AppendUint32(buf, manifest.ChunkCount)
	buf = binary.BigEndian.AppendUint64(buf, manifest.TotalSize)
	buf = binary.BigEndian.AppendUint64(buf, uint64(manifest.CreatedAtUnixNano))
	buf = appendBytes16(buf, []byte(manifest.SnapshotID))
	buf = appendBytes16(buf, []byte(manifest.ChecksumType))
	buf = appendBytes32(buf, confState)
	buf = appendBytes16(buf, manifest.WholeChecksum)
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(manifest.ChunkChecksums)))
	for _, checksum := range manifest.ChunkChecksums {
		buf = appendBytes16(buf, checksum)
	}
	return buf, nil
}

// decodeSnapshotManifest decodes and validates a Pebble snapshot manifest value.
func decodeSnapshotManifest(scope Scope, data []byte) (SnapshotManifest, error) {
	decoder := manifestDecoder{data: data}
	if magic, err := decoder.read(4); err != nil {
		return SnapshotManifest{}, err
	} else if string(magic) != string(snapshotManifestMagic[:]) {
		return SnapshotManifest{}, errors.New("raftstorage: invalid snapshot manifest magic")
	}

	manifest := SnapshotManifest{}
	var err error
	if manifest.Version, err = decoder.readUint16(); err != nil {
		return SnapshotManifest{}, err
	}
	kind, err := decoder.readByte()
	if err != nil {
		return SnapshotManifest{}, err
	}
	manifest.ScopeKind = kind
	if manifest.ScopeID, err = decoder.readUint64(); err != nil {
		return SnapshotManifest{}, err
	}
	if manifest.Index, err = decoder.readUint64(); err != nil {
		return SnapshotManifest{}, err
	}
	if manifest.Term, err = decoder.readUint64(); err != nil {
		return SnapshotManifest{}, err
	}
	if manifest.ChunkSize, err = decoder.readUint64(); err != nil {
		return SnapshotManifest{}, err
	}
	if manifest.ChunkCount, err = decoder.readUint32(); err != nil {
		return SnapshotManifest{}, err
	}
	if manifest.TotalSize, err = decoder.readUint64(); err != nil {
		return SnapshotManifest{}, err
	}
	createdAt, err := decoder.readUint64()
	if err != nil {
		return SnapshotManifest{}, err
	}
	manifest.CreatedAtUnixNano = int64(createdAt)
	snapshotID, err := decoder.readBytes16()
	if err != nil {
		return SnapshotManifest{}, err
	}
	manifest.SnapshotID = string(snapshotID)
	checksumType, err := decoder.readBytes16()
	if err != nil {
		return SnapshotManifest{}, err
	}
	manifest.ChecksumType = string(checksumType)
	confStateBytes, err := decoder.readBytes32()
	if err != nil {
		return SnapshotManifest{}, err
	}
	if len(confStateBytes) > 0 {
		var confState raftpb.ConfState
		if err := confState.Unmarshal(confStateBytes); err != nil {
			return SnapshotManifest{}, fmt.Errorf("raftstorage: invalid snapshot manifest conf state: %w", err)
		}
		manifest.ConfState = cloneConfState(confState)
	}
	wholeChecksum, err := decoder.readBytes16()
	if err != nil {
		return SnapshotManifest{}, err
	}
	manifest.WholeChecksum = append([]byte(nil), wholeChecksum...)
	checksumCount, err := decoder.readUint32()
	if err != nil {
		return SnapshotManifest{}, err
	}
	if err := validateSnapshotManifestShape(manifest, checksumCount); err != nil {
		return SnapshotManifest{}, err
	}
	for i := uint32(0); i < checksumCount; i++ {
		checksum, err := decoder.readBytes16()
		if err != nil {
			return SnapshotManifest{}, err
		}
		manifest.ChunkChecksums = append(manifest.ChunkChecksums, append([]byte(nil), checksum...))
	}
	if decoder.remaining() != 0 {
		return SnapshotManifest{}, errors.New("raftstorage: invalid snapshot manifest trailing data")
	}
	if err := manifest.Validate(scope); err != nil {
		return SnapshotManifest{}, err
	}
	return manifest, nil
}

func validateSnapshotManifestShape(manifest SnapshotManifest, checksumCount uint32) error {
	if manifest.ChunkSize == 0 {
		return errors.New("raftstorage: snapshot manifest chunk size must be non-zero")
	}
	if manifest.TotalSize > uint64(maxInt()) {
		return errors.New("raftstorage: snapshot manifest total size exceeds phase-one limit")
	}
	expectedChunkCount, err := manifestChunkCount(manifest.TotalSize, manifest.ChunkSize)
	if err != nil {
		return err
	}
	if manifest.ChunkCount != expectedChunkCount || checksumCount != manifest.ChunkCount {
		return errors.New("raftstorage: invalid snapshot manifest checksum count")
	}
	return nil
}

func appendBytes16(buf, data []byte) []byte {
	buf = binary.BigEndian.AppendUint16(buf, uint16(len(data)))
	return append(buf, data...)
}

func appendBytes32(buf, data []byte) []byte {
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(data)))
	return append(buf, data...)
}

type manifestDecoder struct {
	data []byte
	off  int
}

func (d *manifestDecoder) remaining() int {
	return len(d.data) - d.off
}

func (d *manifestDecoder) read(size int) ([]byte, error) {
	if size < 0 || d.remaining() < size {
		return nil, errors.New("raftstorage: short snapshot manifest data")
	}
	start := d.off
	d.off += size
	return d.data[start:d.off], nil
}

func (d *manifestDecoder) readByte() (uint8, error) {
	data, err := d.read(1)
	if err != nil {
		return 0, err
	}
	return data[0], nil
}

func (d *manifestDecoder) readUint16() (uint16, error) {
	data, err := d.read(2)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(data), nil
}

func (d *manifestDecoder) readUint32() (uint32, error) {
	data, err := d.read(4)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(data), nil
}

func (d *manifestDecoder) readUint64() (uint64, error) {
	data, err := d.read(8)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint64(data), nil
}

func (d *manifestDecoder) readBytes16() ([]byte, error) {
	size, err := d.readUint16()
	if err != nil {
		return nil, err
	}
	return d.read(int(size))
}

func (d *manifestDecoder) readBytes32() ([]byte, error) {
	size, err := d.readUint32()
	if err != nil {
		return nil, err
	}
	if size > uint32(maxInt()) {
		return nil, errors.New("raftstorage: invalid snapshot manifest data size")
	}
	return d.read(int(size))
}
