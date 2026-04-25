package store

import (
	"encoding/binary"
	"errors"
	"io"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestMessageRowRoundTripPreservesStructuredFields(t *testing.T) {
	row := messageRow{
		MessageSeq:  9,
		MessageID:   42,
		ClientMsgNo: "c-1",
		FromUID:     "u1",
		ChannelID:   "room",
		ChannelType: 1,
		Payload:     []byte("hello"),
		PayloadHash: 123,
	}

	primary, payload, err := encodeMessageFamilies(row)
	require.NoError(t, err)

	decoded, err := decodeMessageFamilies(9, primary, payload)
	require.NoError(t, err)
	require.Equal(t, row.MessageID, decoded.MessageID)
	require.Equal(t, row.Payload, decoded.Payload)
	require.Equal(t, row.PayloadHash, decoded.PayloadHash)
}

func TestMessageRowRejectsZeroMessageID(t *testing.T) {
	_, _, err := encodeMessageFamilies(messageRow{MessageSeq: 1})
	require.Error(t, err)
	require.True(t, errors.Is(err, channel.ErrInvalidArgument) || errors.Is(err, channel.ErrCorruptValue))
}

func TestMessageRowFromChannelMessageCopiesPayloadAndCompatibilityFields(t *testing.T) {
	msg := channel.Message{
		MessageID:   42,
		MessageSeq:  9,
		Framer:      frame.Framer{NoPersist: true, RedDot: true, SyncOnce: true, DUP: true, HasServerVersion: true, End: true},
		Setting:     frame.SettingReceiptEnabled,
		MsgKey:      "k-1",
		Expire:      60,
		ClientSeq:   7,
		ClientMsgNo: "c-1",
		StreamNo:    "s-1",
		StreamID:    88,
		StreamFlag:  frame.StreamFlagIng,
		Timestamp:   99,
		ChannelID:   "room",
		ChannelType: 1,
		Topic:       "topic",
		FromUID:     "u1",
		Payload:     []byte("hello"),
	}

	row := messageRowFromChannelMessage(msg)
	msg.Payload[0] = 'x'

	require.Equal(t, msg.MessageID, row.MessageID)
	require.Equal(t, msg.MessageSeq, row.MessageSeq)
	require.Equal(t, encodeMessageRowFramerFlags(msg.Framer), row.FramerFlags)
	require.Equal(t, uint8(msg.Setting), row.Setting)
	require.Equal(t, uint8(msg.StreamFlag), row.StreamFlag)
	require.Equal(t, msg.MsgKey, row.MsgKey)
	require.Equal(t, msg.ClientMsgNo, row.ClientMsgNo)
	require.Equal(t, msg.ChannelID, row.ChannelID)
	require.Equal(t, []byte("hello"), row.Payload)
	require.Equal(t, hashMessagePayload([]byte("hello")), row.PayloadHash)
}

func TestMessageRowToChannelMessageCopiesPayloadAndCompatibilityFields(t *testing.T) {
	row := messageRow{
		MessageSeq: 9,
		MessageID:  42,
		FramerFlags: encodeMessageRowFramerFlags(frame.Framer{
			NoPersist: true, RedDot: true, SyncOnce: true, DUP: true, HasServerVersion: true, End: true,
		}),
		Setting:     uint8(frame.SettingReceiptEnabled),
		MsgKey:      "k-1",
		Expire:      60,
		ClientSeq:   7,
		ClientMsgNo: "c-1",
		StreamNo:    "s-1",
		StreamID:    88,
		StreamFlag:  uint8(frame.StreamFlagIng),
		Timestamp:   99,
		ChannelID:   "room",
		ChannelType: 1,
		Topic:       "topic",
		FromUID:     "u1",
		Payload:     []byte("hello"),
	}

	msg := row.toChannelMessage()
	msg.Payload[0] = 'x'

	require.Equal(t, row.MessageID, msg.MessageID)
	require.Equal(t, row.MessageSeq, msg.MessageSeq)
	require.Equal(t, decodeMessageRowFramerFlags(row.FramerFlags), msg.Framer)
	require.Equal(t, frame.Setting(row.Setting), msg.Setting)
	require.Equal(t, frame.StreamFlag(row.StreamFlag), msg.StreamFlag)
	require.Equal(t, row.MsgKey, msg.MsgKey)
	require.Equal(t, row.ClientMsgNo, msg.ClientMsgNo)
	require.Equal(t, row.ChannelID, msg.ChannelID)
	require.Equal(t, []byte("hello"), row.Payload)
}

func TestMessageRowFromRecordPayloadDecodesSharedCompatibilityCodec(t *testing.T) {
	msg := channel.Message{
		MessageID:   42,
		Framer:      frame.Framer{NoPersist: true, RedDot: true, SyncOnce: true, DUP: true, HasServerVersion: true, End: true},
		Setting:     frame.SettingReceiptEnabled,
		MsgKey:      "k-1",
		Expire:      60,
		ClientSeq:   7,
		ClientMsgNo: "c-1",
		StreamNo:    "s-1",
		StreamID:    88,
		StreamFlag:  frame.StreamFlagIng,
		Timestamp:   99,
		ChannelID:   "room",
		ChannelType: 1,
		Topic:       "topic",
		FromUID:     "u1",
		Payload:     []byte("hello"),
	}

	payload := makeCompatibilityRecordPayload(t, msg, hashMessagePayload(msg.Payload))

	row, err := decodeCompatibilityRecordPayload(payload)
	require.NoError(t, err)
	payload[len(payload)-1] = 'x'

	require.Equal(t, msg.MessageID, row.MessageID)
	require.Equal(t, encodeMessageRowFramerFlags(msg.Framer), row.FramerFlags)
	require.Equal(t, uint8(msg.Setting), row.Setting)
	require.Equal(t, uint8(msg.StreamFlag), row.StreamFlag)
	require.Equal(t, msg.MsgKey, row.MsgKey)
	require.Equal(t, msg.ClientMsgNo, row.ClientMsgNo)
	require.Equal(t, msg.ChannelID, row.ChannelID)
	require.Equal(t, []byte("hello"), row.Payload)
	require.Equal(t, hashMessagePayload([]byte("hello")), row.PayloadHash)
}

func TestMessageRowToRecordEncodesSharedCompatibilityCodec(t *testing.T) {
	row := messageRow{
		MessageSeq: 9,
		MessageID:  42,
		FramerFlags: encodeMessageRowFramerFlags(frame.Framer{
			NoPersist: true, RedDot: true, SyncOnce: true, DUP: true, HasServerVersion: true, End: true,
		}),
		Setting:     uint8(frame.SettingReceiptEnabled),
		MsgKey:      "k-1",
		Expire:      60,
		ClientSeq:   7,
		ClientMsgNo: "c-1",
		StreamNo:    "s-1",
		StreamID:    88,
		StreamFlag:  uint8(frame.StreamFlagIng),
		Timestamp:   99,
		ChannelID:   "room",
		ChannelType: 1,
		Topic:       "topic",
		FromUID:     "u1",
		Payload:     []byte("hello"),
		PayloadHash: 123,
	}

	record, err := row.toCompatibilityRecord()
	require.NoError(t, err)
	require.Equal(t, len(record.Payload), record.SizeBytes)

	decoded, err := decodeCompatibilityRecordPayload(record.Payload)
	require.NoError(t, err)
	row.Payload[0] = 'x'

	require.Equal(t, row.MessageID, decoded.MessageID)
	require.Equal(t, row.FramerFlags, decoded.FramerFlags)
	require.Equal(t, row.Setting, decoded.Setting)
	require.Equal(t, row.StreamFlag, decoded.StreamFlag)
	require.Equal(t, row.MsgKey, decoded.MsgKey)
	require.Equal(t, row.ClientMsgNo, decoded.ClientMsgNo)
	require.Equal(t, row.ChannelID, decoded.ChannelID)
	require.Equal(t, []byte("hello"), decoded.Payload)
	require.Equal(t, row.PayloadHash, decoded.PayloadHash)
}

func TestMessageRowToRecordRoundTripPreservesHeaderLayout(t *testing.T) {
	row := messageRow{
		MessageID:   42,
		FramerFlags: 3,
		Setting:     4,
		StreamFlag:  5,
		Expire:      6,
		ClientSeq:   7,
		StreamID:    8,
		Timestamp:   9,
		ChannelType: 10,
		ClientMsgNo: "c-1",
		ChannelID:   "room",
		FromUID:     "u1",
		Payload:     []byte("hello"),
	}

	record, err := row.toCompatibilityRecord()
	require.NoError(t, err)

	require.Equal(t, channel.DurableMessageCodecVersion, record.Payload[0])
	require.Equal(t, row.MessageID, binary.BigEndian.Uint64(record.Payload[1:9]))
	require.Equal(t, row.FramerFlags, record.Payload[9])
	require.Equal(t, row.Setting, record.Payload[10])
	require.Equal(t, row.StreamFlag, record.Payload[11])
	require.Equal(t, row.ChannelType, record.Payload[12])
	require.Equal(t, row.Expire, binary.BigEndian.Uint32(record.Payload[13:17]))
	require.Equal(t, row.ClientSeq, binary.BigEndian.Uint64(record.Payload[17:25]))
	require.Equal(t, row.StreamID, binary.BigEndian.Uint64(record.Payload[25:33]))
	require.Equal(t, row.Timestamp, int32(binary.BigEndian.Uint32(record.Payload[33:37])))
}

func TestMessageRowToRecordEncodesExactLegacyFieldOrderAndLengths(t *testing.T) {
	row := messageRow{
		MessageID:   42,
		FramerFlags: encodeMessageRowFramerFlags(frame.Framer{NoPersist: true, End: true}),
		Setting:     uint8(frame.SettingReceiptEnabled),
		StreamFlag:  uint8(frame.StreamFlagIng),
		MsgKey:      "k-1",
		Expire:      60,
		ClientSeq:   7,
		ClientMsgNo: "c-1",
		StreamNo:    "s-1",
		StreamID:    88,
		Timestamp:   99,
		ChannelID:   "room",
		ChannelType: 1,
		Topic:       "topic",
		FromUID:     "u1",
		Payload:     []byte("hello"),
		PayloadHash: 123,
	}

	record, err := row.toCompatibilityRecord()
	require.NoError(t, err)

	offset := channel.DurableMessageHeaderSize
	offset = assertRecordField(t, record.Payload, offset, []byte(row.MsgKey))
	offset = assertRecordField(t, record.Payload, offset, []byte(row.ClientMsgNo))
	offset = assertRecordField(t, record.Payload, offset, []byte(row.StreamNo))
	offset = assertRecordField(t, record.Payload, offset, []byte(row.ChannelID))
	offset = assertRecordField(t, record.Payload, offset, []byte(row.Topic))
	offset = assertRecordField(t, record.Payload, offset, []byte(row.FromUID))
	offset = assertRecordField(t, record.Payload, offset, row.Payload)
	require.Equal(t, len(record.Payload), offset)
	require.Equal(t, row.PayloadHash, binary.BigEndian.Uint64(record.Payload[37:45]))
}

func TestMessageRowFromRecordPayloadDecodesExactLegacyWireLayout(t *testing.T) {
	payload := makeCompatibilityRecordPayload(t, channel.Message{
		MessageID:   42,
		Framer:      frame.Framer{NoPersist: true, End: true},
		Setting:     frame.SettingReceiptEnabled,
		MsgKey:      "k-1",
		Expire:      60,
		ClientSeq:   7,
		ClientMsgNo: "c-1",
		StreamNo:    "s-1",
		StreamID:    88,
		StreamFlag:  frame.StreamFlagIng,
		Timestamp:   99,
		ChannelID:   "room",
		ChannelType: 1,
		Topic:       "topic",
		FromUID:     "u1",
		Payload:     []byte("hello"),
	}, 123)

	row, err := decodeCompatibilityRecordPayload(payload)
	require.NoError(t, err)
	require.Equal(t, uint64(42), row.MessageID)
	require.Equal(t, encodeMessageRowFramerFlags(frame.Framer{NoPersist: true, End: true}), row.FramerFlags)
	require.Equal(t, uint8(frame.SettingReceiptEnabled), row.Setting)
	require.Equal(t, uint8(frame.StreamFlagIng), row.StreamFlag)
	require.Equal(t, "k-1", row.MsgKey)
	require.Equal(t, uint32(60), row.Expire)
	require.Equal(t, uint64(7), row.ClientSeq)
	require.Equal(t, "c-1", row.ClientMsgNo)
	require.Equal(t, "s-1", row.StreamNo)
	require.Equal(t, uint64(88), row.StreamID)
	require.Equal(t, int32(99), row.Timestamp)
	require.Equal(t, "room", row.ChannelID)
	require.Equal(t, uint8(1), row.ChannelType)
	require.Equal(t, "topic", row.Topic)
	require.Equal(t, "u1", row.FromUID)
	require.Equal(t, []byte("hello"), row.Payload)
	require.Equal(t, uint64(123), row.PayloadHash)
}

func TestMessageRowFromRecordPayloadRejectsTruncatedLengthPrefixedField(t *testing.T) {
	payload := makeCompatibilityRecordPayload(t, channel.Message{
		MessageID:   42,
		ClientMsgNo: "c-1",
		ChannelID:   "room",
		FromUID:     "u1",
		Payload:     []byte("hello"),
	}, 123)
	truncated := append([]byte(nil), payload[:len(payload)-2]...)

	_, err := decodeCompatibilityRecordPayload(truncated)
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
}

func makeCompatibilityRecordPayload(t *testing.T, msg channel.Message, payloadHash uint64) []byte {
	t.Helper()

	payload := []byte{channel.DurableMessageCodecVersion}
	payload = binary.BigEndian.AppendUint64(payload, msg.MessageID)
	payload = append(payload, encodeMessageRowFramerFlags(msg.Framer))
	payload = append(payload, byte(msg.Setting))
	payload = append(payload, byte(msg.StreamFlag))
	payload = append(payload, msg.ChannelType)
	payload = binary.BigEndian.AppendUint32(payload, msg.Expire)
	payload = binary.BigEndian.AppendUint64(payload, msg.ClientSeq)
	payload = binary.BigEndian.AppendUint64(payload, msg.StreamID)
	payload = binary.BigEndian.AppendUint32(payload, uint32(msg.Timestamp))
	payload = binary.BigEndian.AppendUint64(payload, payloadHash)
	payload = appendRecordField(payload, []byte(msg.MsgKey))
	payload = appendRecordField(payload, []byte(msg.ClientMsgNo))
	payload = appendRecordField(payload, []byte(msg.StreamNo))
	payload = appendRecordField(payload, []byte(msg.ChannelID))
	payload = appendRecordField(payload, []byte(msg.Topic))
	payload = appendRecordField(payload, []byte(msg.FromUID))
	payload = appendRecordField(payload, msg.Payload)
	return payload
}

func assertRecordField(t *testing.T, payload []byte, offset int, want []byte) int {
	t.Helper()

	require.GreaterOrEqual(t, len(payload[offset:]), 4)
	length := int(binary.BigEndian.Uint32(payload[offset : offset+4]))
	offset += 4
	require.Equal(t, len(want), length)
	require.GreaterOrEqual(t, len(payload[offset:]), length)
	require.Equal(t, want, payload[offset:offset+length])
	return offset + length
}

func appendRecordField(dst []byte, value []byte) []byte {
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(value)))
	return append(dst, value...)
}
