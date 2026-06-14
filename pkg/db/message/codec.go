package message

import (
	"encoding/binary"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/rowcodec"
)

const messageValueVersion byte = 1

func encodeMessageHeader(key []byte, row messageRow) ([]byte, error) {
	row = normalizeMessageRow(row)
	if err := row.validate(); err != nil {
		return nil, err
	}
	var w rowcodec.Writer
	if err := w.Uint64(messageColumnIDMessageID, row.MessageID); err != nil {
		return nil, err
	}
	if err := w.Uint8(messageColumnIDFramerFlags, row.FramerFlags); err != nil {
		return nil, err
	}
	if err := w.Uint8(messageColumnIDSetting, row.Setting); err != nil {
		return nil, err
	}
	if err := w.Uint8(messageColumnIDStreamFlag, row.StreamFlag); err != nil {
		return nil, err
	}
	if err := w.String(messageColumnIDMsgKey, row.MsgKey); err != nil {
		return nil, err
	}
	if err := w.Uint64(messageColumnIDExpire, row.Expire); err != nil {
		return nil, err
	}
	if err := w.Uint64(messageColumnIDClientSeq, row.ClientSeq); err != nil {
		return nil, err
	}
	if err := w.String(messageColumnIDClientMsgNo, row.ClientMsgNo); err != nil {
		return nil, err
	}
	if err := w.String(messageColumnIDStreamNo, row.StreamNo); err != nil {
		return nil, err
	}
	if err := w.Uint64(messageColumnIDStreamID, row.StreamID); err != nil {
		return nil, err
	}
	if err := w.Int64(messageColumnIDTimestamp, row.Timestamp); err != nil {
		return nil, err
	}
	if err := w.String(messageColumnIDChannelID, row.ChannelID); err != nil {
		return nil, err
	}
	if err := w.Uint8(messageColumnIDChannelType, row.ChannelType); err != nil {
		return nil, err
	}
	if err := w.String(messageColumnIDTopic, row.Topic); err != nil {
		return nil, err
	}
	if err := w.String(messageColumnIDFromUID, row.FromUID); err != nil {
		return nil, err
	}
	if err := w.Uint64(messageColumnIDPayloadHash, row.PayloadHash); err != nil {
		return nil, err
	}
	if err := w.Uint64(messageColumnIDPayloadSize, row.PayloadSize); err != nil {
		return nil, err
	}
	if err := w.Int64(messageColumnIDServerTimestampMS, row.ServerTimestampMS); err != nil {
		return nil, err
	}
	return rowcodec.Wrap(key, messageValueVersion, rowcodec.CodecColumns, rowcodec.FlagChecksum, w.Bytes()), nil
}

func encodedMessageHeaderLen(row messageRow) int {
	row = normalizeMessageRow(row)
	payloadLen := 0
	var last uint16
	payloadLen += encodedUint64ColumnLen(&last, messageColumnIDMessageID, row.MessageID)
	payloadLen += encodedUint8ColumnLen(&last, messageColumnIDFramerFlags, row.FramerFlags)
	payloadLen += encodedUint8ColumnLen(&last, messageColumnIDSetting, row.Setting)
	payloadLen += encodedUint8ColumnLen(&last, messageColumnIDStreamFlag, row.StreamFlag)
	payloadLen += encodedStringColumnLen(&last, messageColumnIDMsgKey, row.MsgKey)
	payloadLen += encodedUint64ColumnLen(&last, messageColumnIDExpire, row.Expire)
	payloadLen += encodedUint64ColumnLen(&last, messageColumnIDClientSeq, row.ClientSeq)
	payloadLen += encodedStringColumnLen(&last, messageColumnIDClientMsgNo, row.ClientMsgNo)
	payloadLen += encodedStringColumnLen(&last, messageColumnIDStreamNo, row.StreamNo)
	payloadLen += encodedUint64ColumnLen(&last, messageColumnIDStreamID, row.StreamID)
	payloadLen += encodedInt64ColumnLen(&last, messageColumnIDTimestamp, row.Timestamp)
	payloadLen += encodedStringColumnLen(&last, messageColumnIDChannelID, row.ChannelID)
	payloadLen += encodedUint8ColumnLen(&last, messageColumnIDChannelType, row.ChannelType)
	payloadLen += encodedStringColumnLen(&last, messageColumnIDTopic, row.Topic)
	payloadLen += encodedStringColumnLen(&last, messageColumnIDFromUID, row.FromUID)
	payloadLen += encodedUint64ColumnLen(&last, messageColumnIDPayloadHash, row.PayloadHash)
	payloadLen += encodedUint64ColumnLen(&last, messageColumnIDPayloadSize, row.PayloadSize)
	payloadLen += encodedInt64ColumnLen(&last, messageColumnIDServerTimestampMS, row.ServerTimestampMS)
	return rowcodec.EnvelopeLen(payloadLen)
}

func encodeMessageHeaderTo(dst []byte, key []byte, row messageRow) error {
	row = normalizeMessageRow(row)
	if err := row.validate(); err != nil {
		return err
	}
	if len(dst) != encodedMessageHeaderLen(row) {
		return dberrors.ErrInvalidArgument
	}
	payload := dst[rowcodec.EnvelopeLen(0):]
	pos := 0
	var last uint16
	pos = putUint64Column(payload, pos, &last, messageColumnIDMessageID, row.MessageID)
	pos = putUint8Column(payload, pos, &last, messageColumnIDFramerFlags, row.FramerFlags)
	pos = putUint8Column(payload, pos, &last, messageColumnIDSetting, row.Setting)
	pos = putUint8Column(payload, pos, &last, messageColumnIDStreamFlag, row.StreamFlag)
	pos = putStringColumn(payload, pos, &last, messageColumnIDMsgKey, row.MsgKey)
	pos = putUint64Column(payload, pos, &last, messageColumnIDExpire, row.Expire)
	pos = putUint64Column(payload, pos, &last, messageColumnIDClientSeq, row.ClientSeq)
	pos = putStringColumn(payload, pos, &last, messageColumnIDClientMsgNo, row.ClientMsgNo)
	pos = putStringColumn(payload, pos, &last, messageColumnIDStreamNo, row.StreamNo)
	pos = putUint64Column(payload, pos, &last, messageColumnIDStreamID, row.StreamID)
	pos = putInt64Column(payload, pos, &last, messageColumnIDTimestamp, row.Timestamp)
	pos = putStringColumn(payload, pos, &last, messageColumnIDChannelID, row.ChannelID)
	pos = putUint8Column(payload, pos, &last, messageColumnIDChannelType, row.ChannelType)
	pos = putStringColumn(payload, pos, &last, messageColumnIDTopic, row.Topic)
	pos = putStringColumn(payload, pos, &last, messageColumnIDFromUID, row.FromUID)
	pos = putUint64Column(payload, pos, &last, messageColumnIDPayloadHash, row.PayloadHash)
	pos = putUint64Column(payload, pos, &last, messageColumnIDPayloadSize, row.PayloadSize)
	pos = putInt64Column(payload, pos, &last, messageColumnIDServerTimestampMS, row.ServerTimestampMS)
	if pos != len(payload) {
		return dberrors.ErrInvalidArgument
	}
	return rowcodec.WrapTo(dst, key, messageValueVersion, rowcodec.CodecColumns, rowcodec.FlagChecksum, payload)
}

func decodeMessageHeader(key []byte, value []byte, row *messageRow) error {
	if row == nil {
		return dberrors.ErrInvalidArgument
	}
	env, err := rowcodec.Unwrap(key, value)
	if err != nil {
		return err
	}
	if env.Version != messageValueVersion || env.Codec != rowcodec.CodecColumns {
		return fmt.Errorf("%w: invalid message header envelope", dberrors.ErrCorruptValue)
	}
	s := rowcodec.NewScanner(env.Payload)
	for s.Next() {
		if err := decodeMessageHeaderColumn(s, row); err != nil {
			return err
		}
	}
	if err := s.Err(); err != nil {
		return err
	}
	if row.MessageID == 0 {
		return fmt.Errorf("%w: missing message_id", dberrors.ErrCorruptValue)
	}
	return nil
}

func decodeMessageHeaderColumn(s *rowcodec.Scanner, row *messageRow) error {
	switch s.ColumnID() {
	case messageColumnIDMessageID:
		value, err := s.Uint64()
		row.MessageID = value
		return err
	case messageColumnIDFramerFlags:
		value, err := s.Uint8()
		row.FramerFlags = value
		return err
	case messageColumnIDSetting:
		value, err := s.Uint8()
		row.Setting = value
		return err
	case messageColumnIDStreamFlag:
		value, err := s.Uint8()
		row.StreamFlag = value
		return err
	case messageColumnIDMsgKey:
		value, err := s.String()
		row.MsgKey = value
		return err
	case messageColumnIDExpire:
		value, err := s.Uint64()
		row.Expire = value
		return err
	case messageColumnIDClientSeq:
		value, err := s.Uint64()
		row.ClientSeq = value
		return err
	case messageColumnIDClientMsgNo:
		value, err := s.String()
		row.ClientMsgNo = value
		return err
	case messageColumnIDStreamNo:
		value, err := s.String()
		row.StreamNo = value
		return err
	case messageColumnIDStreamID:
		value, err := s.Uint64()
		row.StreamID = value
		return err
	case messageColumnIDTimestamp:
		value, err := s.Int64()
		row.Timestamp = value
		return err
	case messageColumnIDChannelID:
		value, err := s.String()
		row.ChannelID = value
		return err
	case messageColumnIDChannelType:
		value, err := s.Uint8()
		row.ChannelType = value
		return err
	case messageColumnIDTopic:
		value, err := s.String()
		row.Topic = value
		return err
	case messageColumnIDFromUID:
		value, err := s.String()
		row.FromUID = value
		return err
	case messageColumnIDPayloadHash:
		value, err := s.Uint64()
		row.PayloadHash = value
		return err
	case messageColumnIDPayloadSize:
		value, err := s.Uint64()
		row.PayloadSize = value
		return err
	case messageColumnIDServerTimestampMS:
		value, err := s.Int64()
		row.ServerTimestampMS = value
		return err
	default:
		return nil
	}
}

func encodeMessagePayload(key []byte, row messageRow) ([]byte, error) {
	row = normalizeMessageRow(row)
	if err := row.validate(); err != nil {
		return nil, err
	}
	var w rowcodec.Writer
	if err := w.RawBytes(messageColumnIDPayload, row.Payload); err != nil {
		return nil, err
	}
	return rowcodec.Wrap(key, messageValueVersion, rowcodec.CodecColumns, rowcodec.FlagChecksum, w.Bytes()), nil
}

func encodedMessagePayloadLen(row messageRow) int {
	payloadLen := encodedMessagePayloadColumnLen(len(row.Payload))
	return rowcodec.EnvelopeLen(payloadLen)
}

func encodedMessagePayloadColumnLen(payloadLen int) int {
	return 1 + uvarintLen(uint64(messageColumnIDPayload)) + uvarintLen(uint64(payloadLen)) + payloadLen
}

func encodeMessagePayloadTo(dst []byte, key []byte, row messageRow) error {
	row = normalizeMessageRow(row)
	if err := row.validate(); err != nil {
		return err
	}
	if len(dst) != encodedMessagePayloadLen(row) {
		return dberrors.ErrInvalidArgument
	}
	payload := dst[rowcodec.EnvelopeLen(0):]
	payload[0] = byte(rowcodec.TypeBytes)
	pos := 1
	pos += binary.PutUvarint(payload[pos:], uint64(messageColumnIDPayload))
	pos += binary.PutUvarint(payload[pos:], uint64(len(row.Payload)))
	copy(payload[pos:], row.Payload)
	return rowcodec.WrapTo(dst, key, messageValueVersion, rowcodec.CodecColumns, rowcodec.FlagChecksum, payload)
}

func uvarintLen(value uint64) int {
	var buf [binary.MaxVarintLen64]byte
	return binary.PutUvarint(buf[:], value)
}

func encodedColumnBeginLen(last *uint16, columnID uint16) int {
	delta := columnID - *last
	*last = columnID
	if delta <= 15 {
		return 1
	}
	return 1 + uvarintLen(uint64(delta))
}

func encodedStringColumnLen(last *uint16, columnID uint16, value string) int {
	return encodedColumnBeginLen(last, columnID) + uvarintLen(uint64(len(value))) + len(value)
}

func encodedUint64ColumnLen(last *uint16, columnID uint16, value uint64) int {
	return encodedColumnBeginLen(last, columnID) + uvarintLen(value)
}

func encodedInt64ColumnLen(last *uint16, columnID uint16, value int64) int {
	return encodedColumnBeginLen(last, columnID) + uvarintLen(encodeZigZagInt64(value))
}

func encodedUint8ColumnLen(last *uint16, columnID uint16, value uint8) int {
	return encodedColumnBeginLen(last, columnID) + 1
}

func putColumnBegin(dst []byte, pos int, last *uint16, columnID uint16, typ rowcodec.Type) int {
	delta := columnID - *last
	*last = columnID
	if delta <= 15 {
		dst[pos] = byte(delta<<4) | byte(typ)
		return pos + 1
	}
	dst[pos] = byte(typ)
	pos++
	return pos + binary.PutUvarint(dst[pos:], uint64(delta))
}

func putStringColumn(dst []byte, pos int, last *uint16, columnID uint16, value string) int {
	pos = putColumnBegin(dst, pos, last, columnID, rowcodec.TypeString)
	pos += binary.PutUvarint(dst[pos:], uint64(len(value)))
	copy(dst[pos:], value)
	return pos + len(value)
}

func putUint64Column(dst []byte, pos int, last *uint16, columnID uint16, value uint64) int {
	pos = putColumnBegin(dst, pos, last, columnID, rowcodec.TypeUint64)
	return pos + binary.PutUvarint(dst[pos:], value)
}

func putInt64Column(dst []byte, pos int, last *uint16, columnID uint16, value int64) int {
	pos = putColumnBegin(dst, pos, last, columnID, rowcodec.TypeInt64)
	return pos + binary.PutUvarint(dst[pos:], encodeZigZagInt64(value))
}

func putUint8Column(dst []byte, pos int, last *uint16, columnID uint16, value uint8) int {
	pos = putColumnBegin(dst, pos, last, columnID, rowcodec.TypeUint8)
	dst[pos] = value
	return pos + 1
}

func encodeZigZagInt64(value int64) uint64 {
	return uint64(value<<1) ^ uint64(value>>63)
}

func decodeMessagePayload(key []byte, value []byte, row *messageRow) error {
	if row == nil {
		return dberrors.ErrInvalidArgument
	}
	env, err := rowcodec.Unwrap(key, value)
	if err != nil {
		return err
	}
	if env.Version != messageValueVersion || env.Codec != rowcodec.CodecColumns {
		return fmt.Errorf("%w: invalid message payload envelope", dberrors.ErrCorruptValue)
	}
	s := rowcodec.NewScanner(env.Payload)
	for s.Next() {
		if s.ColumnID() != messageColumnIDPayload {
			continue
		}
		payload, err := s.Bytes()
		if err != nil {
			return err
		}
		row.Payload = payload
	}
	return s.Err()
}
