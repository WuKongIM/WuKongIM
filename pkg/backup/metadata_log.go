package backup

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

const (
	// MetadataLogRecordVersion is the portable committed Slot-command schema.
	MetadataLogRecordVersion     uint16 = 1
	metadataLogRecordHeaderBytes        = 4 + 2 + 2 + 8 + 8 + 8 + 4
)

var metadataLogRecordMagic = [4]byte{'W', 'K', 'M', 'D'}

// MetadataLogRecord is one committed logical Hash Slot state-machine command.
type MetadataLogRecord struct {
	// HashSlot identifies the logical metadata partition affected by Command.
	HashSlot uint16
	// RaftIndex and RaftTerm bind Command to its committed physical Slot order.
	RaftIndex uint64
	RaftTerm  uint64
	// CommittedAtUnixMillis is the proposer-issued UTC command time.
	CommittedAtUnixMillis int64
	// Command is the exact portable Slot FSM command payload without its Raft envelope.
	Command []byte
}

// MarshalMetadataLogRecord encodes one strict portable committed metadata record.
func MarshalMetadataLogRecord(record MetadataLogRecord) ([]byte, error) {
	if record.RaftIndex == 0 || record.RaftTerm == 0 ||
		record.CommittedAtUnixMillis <= 0 || len(record.Command) == 0 ||
		len(record.Command) > maxObjectPlaintextBytes-metadataLogRecordHeaderBytes {
		return nil, fmt.Errorf("%w: metadata log record is invalid", ErrInvalidObject)
	}
	body := make([]byte, 0, metadataLogRecordHeaderBytes+len(record.Command))
	body = append(body, metadataLogRecordMagic[:]...)
	body = binary.BigEndian.AppendUint16(body, MetadataLogRecordVersion)
	body = binary.BigEndian.AppendUint16(body, record.HashSlot)
	body = binary.BigEndian.AppendUint64(body, record.RaftIndex)
	body = binary.BigEndian.AppendUint64(body, record.RaftTerm)
	body = binary.BigEndian.AppendUint64(body, uint64(record.CommittedAtUnixMillis))
	body = binary.BigEndian.AppendUint32(body, uint32(len(record.Command)))
	body = append(body, record.Command...)
	return body, nil
}

// LoadMetadataLogRecord strictly decodes one committed metadata command.
func LoadMetadataLogRecord(body []byte) (MetadataLogRecord, error) {
	if len(body) < metadataLogRecordHeaderBytes || len(body) > maxObjectPlaintextBytes ||
		!bytes.Equal(body[:4], metadataLogRecordMagic[:]) ||
		binary.BigEndian.Uint16(body[4:6]) != MetadataLogRecordVersion {
		return MetadataLogRecord{}, fmt.Errorf("%w: metadata log record header is invalid", ErrInvalidObject)
	}
	commandBytes := int(binary.BigEndian.Uint32(body[32:36]))
	if commandBytes == 0 || commandBytes != len(body)-metadataLogRecordHeaderBytes {
		return MetadataLogRecord{}, fmt.Errorf("%w: metadata log record length is invalid", ErrInvalidObject)
	}
	record := MetadataLogRecord{
		HashSlot:              binary.BigEndian.Uint16(body[6:8]),
		RaftIndex:             binary.BigEndian.Uint64(body[8:16]),
		RaftTerm:              binary.BigEndian.Uint64(body[16:24]),
		CommittedAtUnixMillis: int64(binary.BigEndian.Uint64(body[24:32])),
		Command:               append([]byte(nil), body[metadataLogRecordHeaderBytes:]...),
	}
	if record.RaftIndex == 0 || record.RaftTerm == 0 || record.CommittedAtUnixMillis <= 0 {
		return MetadataLogRecord{}, fmt.Errorf("%w: metadata log record boundary is invalid", ErrInvalidObject)
	}
	return record, nil
}
