package raftstore

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"

	"go.etcd.io/raft/v3/raftpb"
)

var (
	// ErrCRCMismatch indicates that a WAL record failed rolling CRC validation.
	ErrCRCMismatch = errors.New("controllerv2/raftstore: crc mismatch")
	// ErrTruncatedRecord indicates that the WAL ended in the middle of a frame.
	ErrTruncatedRecord = errors.New("controllerv2/raftstore: truncated record")
)

type recordType uint8

const (
	recordSegmentHeader recordType = iota + 1
	recordEntries
	recordHardState
	recordSnapshot
	recordAppliedIndex
)

type walRecord struct {
	Type    recordType
	Payload []byte
}

var crcTable = crc32.MakeTable(crc32.Castagnoli)

func recordCRC(prev uint32, typ recordType, payload []byte) uint32 {
	h := crc32.New(crcTable)
	var seed [4]byte
	binary.BigEndian.PutUint32(seed[:], prev)
	_, _ = h.Write(seed[:])
	_, _ = h.Write([]byte{byte(typ)})
	_, _ = h.Write(payload)
	return h.Sum32()
}

func writeRecord(w io.Writer, rec walRecord, prevCRC uint32) error {
	crc := recordCRC(prevCRC, rec.Type, rec.Payload)
	length := uint32(1 + 4 + len(rec.Payload))
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], length)
	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	if _, err := w.Write([]byte{byte(rec.Type)}); err != nil {
		return err
	}
	binary.BigEndian.PutUint32(header[:], crc)
	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	_, err := w.Write(rec.Payload)
	return err
}

func readRecord(r io.Reader, prevCRC uint32) (walRecord, uint32, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		if errors.Is(err, io.EOF) {
			return walRecord{}, prevCRC, io.EOF
		}
		return walRecord{}, prevCRC, ErrTruncatedRecord
	}
	length := binary.BigEndian.Uint32(lenBuf[:])
	if length < 5 {
		return walRecord{}, prevCRC, ErrTruncatedRecord
	}
	frame := make([]byte, length)
	if _, err := io.ReadFull(r, frame); err != nil {
		return walRecord{}, prevCRC, ErrTruncatedRecord
	}
	typ := recordType(frame[0])
	wantCRC := binary.BigEndian.Uint32(frame[1:5])
	payload := append([]byte(nil), frame[5:]...)
	gotCRC := recordCRC(prevCRC, typ, payload)
	if gotCRC != wantCRC {
		return walRecord{}, prevCRC, ErrCRCMismatch
	}
	return walRecord{Type: typ, Payload: payload}, gotCRC, nil
}

func marshalEntryRecord(entries []raftpb.Entry) ([]byte, error) {
	var buf bytes.Buffer
	var tmp [4]byte
	binary.BigEndian.PutUint32(tmp[:], uint32(len(entries)))
	buf.Write(tmp[:])
	for _, entry := range entries {
		data, err := entry.Marshal()
		if err != nil {
			return nil, err
		}
		binary.BigEndian.PutUint32(tmp[:], uint32(len(data)))
		buf.Write(tmp[:])
		buf.Write(data)
	}
	return buf.Bytes(), nil
}

func unmarshalEntryRecord(payload []byte) ([]raftpb.Entry, error) {
	if len(payload) < 4 {
		return nil, ErrTruncatedRecord
	}
	count := binary.BigEndian.Uint32(payload[:4])
	payload = payload[4:]
	entries := make([]raftpb.Entry, 0, count)
	for i := uint32(0); i < count; i++ {
		if len(payload) < 4 {
			return nil, ErrTruncatedRecord
		}
		size := binary.BigEndian.Uint32(payload[:4])
		payload = payload[4:]
		if uint32(len(payload)) < size {
			return nil, ErrTruncatedRecord
		}
		var entry raftpb.Entry
		if err := entry.Unmarshal(payload[:size]); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
		payload = payload[size:]
	}
	return entries, nil
}

func marshalHardStateRecord(hs raftpb.HardState) ([]byte, error) {
	return hs.Marshal()
}

func unmarshalHardStateRecord(payload []byte) (raftpb.HardState, error) {
	var hs raftpb.HardState
	return hs, hs.Unmarshal(payload)
}

func marshalSnapshotRecord(meta raftpb.SnapshotMetadata) ([]byte, error) {
	return meta.Marshal()
}

func unmarshalSnapshotRecord(payload []byte) (raftpb.SnapshotMetadata, error) {
	var meta raftpb.SnapshotMetadata
	return meta, meta.Unmarshal(payload)
}

func marshalUint64(v uint64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	return b[:]
}

func unmarshalUint64(payload []byte) (uint64, error) {
	if len(payload) != 8 {
		return 0, ErrTruncatedRecord
	}
	return binary.BigEndian.Uint64(payload), nil
}
