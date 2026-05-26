package message

import (
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
	return rowcodec.Wrap(key, messageValueVersion, rowcodec.CodecColumns, rowcodec.FlagChecksum, w.Bytes()), nil
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
