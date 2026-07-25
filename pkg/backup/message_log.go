package backup

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"unicode/utf8"
)

const (
	// MessageLogRecordVersion is the portable committed-message record schema.
	MessageLogRecordVersion uint16 = 1

	// MessageLogRecordMessage carries one committed Channel log row.
	MessageLogRecordMessage uint8 = 1
	// MessageLogRecordBoundary carries an epoch or retention-only cursor change.
	MessageLogRecordBoundary uint8 = 2

	maxMessageLogIdentityBytes = 4 << 10
	messageLogFixedBytes       = 4 + 2 + 1 + 2 + 1 + 1 + 1 + 8 + 8 + 8 + 8 + 8 + 8 + 4 + 4 + 4 + 4 + 4
)

var messageLogRecordMagic = [4]byte{'W', 'K', 'M', 'G'}

// MessageLogRecord is one portable row from an authoritative committed Channel
// log. Boundary-only records preserve cursor changes without inventing payload.
type MessageLogRecord struct {
	Kind        uint8
	HashSlot    uint16
	ChannelID   string
	ChannelType uint8
	Epoch       uint64
	// LogStartOffset and HW are the exact committed Channel cut observed by capture.
	LogStartOffset uint64
	HW             uint64
	// Message fields are zero for a boundary-only record.
	MessageSeq        uint64
	MessageID         uint64
	Setting           uint8
	FromUID           string
	ClientMsgNo       string
	ServerTimestampMS int64
	SyncOnce          bool
	Payload           []byte
}

// MarshalMessageLogRecord encodes one bounded portable committed-message row.
func MarshalMessageLogRecord(record MessageLogRecord) ([]byte, error) {
	if err := validateMessageLogRecord(record); err != nil {
		return nil, err
	}
	total := messageLogFixedBytes + len(record.ChannelID) + len(record.FromUID) + len(record.ClientMsgNo) + len(record.Payload)
	if total > maxObjectPlaintextBytes {
		return nil, fmt.Errorf("%w: message log record exceeds size limit", ErrInvalidObject)
	}
	body := make([]byte, 0, total)
	body = append(body, messageLogRecordMagic[:]...)
	body = binary.BigEndian.AppendUint16(body, MessageLogRecordVersion)
	body = append(body, record.Kind)
	body = binary.BigEndian.AppendUint16(body, record.HashSlot)
	body = append(body, record.ChannelType, record.Setting)
	if record.SyncOnce {
		body = append(body, 1)
	} else {
		body = append(body, 0)
	}
	body = binary.BigEndian.AppendUint64(body, record.Epoch)
	body = binary.BigEndian.AppendUint64(body, record.LogStartOffset)
	body = binary.BigEndian.AppendUint64(body, record.HW)
	body = binary.BigEndian.AppendUint64(body, record.MessageSeq)
	body = binary.BigEndian.AppendUint64(body, record.MessageID)
	body = binary.BigEndian.AppendUint64(body, uint64(record.ServerTimestampMS))
	body = appendMessageLogBytes(body, []byte(record.ChannelID))
	body = appendMessageLogBytes(body, []byte(record.FromUID))
	body = appendMessageLogBytes(body, []byte(record.ClientMsgNo))
	body = appendMessageLogBytes(body, record.Payload)
	body = binary.BigEndian.AppendUint32(body, crc32.ChecksumIEEE(body))
	return body, nil
}

// LoadMessageLogRecord strictly decodes one portable committed-message row.
func LoadMessageLogRecord(body []byte) (MessageLogRecord, error) {
	if len(body) < messageLogFixedBytes || len(body) > maxObjectPlaintextBytes ||
		!bytes.Equal(body[:4], messageLogRecordMagic[:]) ||
		binary.BigEndian.Uint16(body[4:6]) != MessageLogRecordVersion ||
		crc32.ChecksumIEEE(body[:len(body)-4]) != binary.BigEndian.Uint32(body[len(body)-4:]) {
		return MessageLogRecord{}, fmt.Errorf("%w: message log record header is invalid", ErrInvalidObject)
	}
	reader := bytes.NewReader(body[6 : len(body)-4])
	record := MessageLogRecord{}
	var syncOnce uint8
	if binary.Read(reader, binary.BigEndian, &record.Kind) != nil ||
		binary.Read(reader, binary.BigEndian, &record.HashSlot) != nil ||
		binary.Read(reader, binary.BigEndian, &record.ChannelType) != nil ||
		binary.Read(reader, binary.BigEndian, &record.Setting) != nil ||
		binary.Read(reader, binary.BigEndian, &syncOnce) != nil ||
		binary.Read(reader, binary.BigEndian, &record.Epoch) != nil ||
		binary.Read(reader, binary.BigEndian, &record.LogStartOffset) != nil ||
		binary.Read(reader, binary.BigEndian, &record.HW) != nil ||
		binary.Read(reader, binary.BigEndian, &record.MessageSeq) != nil ||
		binary.Read(reader, binary.BigEndian, &record.MessageID) != nil {
		return MessageLogRecord{}, fmt.Errorf("%w: message log record is truncated", ErrInvalidObject)
	}
	var timestamp uint64
	if binary.Read(reader, binary.BigEndian, &timestamp) != nil || timestamp > math.MaxInt64 || syncOnce > 1 {
		return MessageLogRecord{}, fmt.Errorf("%w: message log record flags are invalid", ErrInvalidObject)
	}
	record.ServerTimestampMS = int64(timestamp)
	record.SyncOnce = syncOnce == 1
	var err error
	if record.ChannelID, err = readMessageLogString(reader); err != nil {
		return MessageLogRecord{}, err
	}
	if record.FromUID, err = readMessageLogString(reader); err != nil {
		return MessageLogRecord{}, err
	}
	if record.ClientMsgNo, err = readMessageLogString(reader); err != nil {
		return MessageLogRecord{}, err
	}
	if record.Payload, err = readMessageLogBytes(reader); err != nil || reader.Len() != 0 {
		return MessageLogRecord{}, fmt.Errorf("%w: message log record payload is invalid", ErrInvalidObject)
	}
	if err := validateMessageLogRecord(record); err != nil {
		return MessageLogRecord{}, err
	}
	return record, nil
}

func validateMessageLogRecord(record MessageLogRecord) error {
	if record.ChannelID == "" || len(record.ChannelID) > maxMessageLogIdentityBytes ||
		!utf8.ValidString(record.ChannelID) || len(record.FromUID) > maxMessageLogIdentityBytes ||
		!utf8.ValidString(record.FromUID) || len(record.ClientMsgNo) > maxMessageLogIdentityBytes ||
		!utf8.ValidString(record.ClientMsgNo) || record.Epoch == 0 ||
		record.LogStartOffset > record.HW || record.ServerTimestampMS < 0 {
		return fmt.Errorf("%w: message log record boundary is invalid", ErrInvalidObject)
	}
	switch record.Kind {
	case MessageLogRecordMessage:
		if record.MessageSeq == 0 || record.MessageID == 0 ||
			record.MessageSeq <= record.LogStartOffset || record.MessageSeq > record.HW {
			return fmt.Errorf("%w: committed message identity is invalid", ErrInvalidObject)
		}
	case MessageLogRecordBoundary:
		if record.MessageSeq != 0 || record.MessageID != 0 || record.Setting != 0 ||
			record.FromUID != "" || record.ClientMsgNo != "" ||
			record.ServerTimestampMS != 0 || record.SyncOnce || len(record.Payload) != 0 {
			return fmt.Errorf("%w: boundary-only message record has payload", ErrInvalidObject)
		}
	default:
		return fmt.Errorf("%w: message log record kind is invalid", ErrInvalidObject)
	}
	return nil
}

func appendMessageLogBytes(body, value []byte) []byte {
	body = binary.BigEndian.AppendUint32(body, uint32(len(value)))
	return append(body, value...)
}

func readMessageLogString(reader *bytes.Reader) (string, error) {
	value, err := readMessageLogBytes(reader)
	if err != nil || len(value) > maxMessageLogIdentityBytes || !utf8.Valid(value) {
		return "", fmt.Errorf("%w: message log string is invalid", ErrInvalidObject)
	}
	return string(value), nil
}

func readMessageLogBytes(reader *bytes.Reader) ([]byte, error) {
	var size uint32
	if err := binary.Read(reader, binary.BigEndian, &size); err != nil || uint64(size) > uint64(reader.Len()) {
		return nil, fmt.Errorf("%w: message log bytes are truncated", ErrInvalidObject)
	}
	value := make([]byte, int(size))
	if _, err := io.ReadFull(reader, value); err != nil {
		return nil, fmt.Errorf("%w: message log bytes are truncated", ErrInvalidObject)
	}
	return value, nil
}
