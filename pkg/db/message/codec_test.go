package message

import (
	"encoding/binary"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/rowcodec"
	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
)

func TestMessageHeaderCodecRoundTrip(t *testing.T) {
	row := testMessageRow()
	key := encodeMessageRowKey(ChannelKey("ch-1"), row.MessageSeq, messageHeaderFamilyID)
	value, err := encodeMessageHeader(key, row)
	if err != nil {
		t.Fatalf("encodeMessageHeader(): %v", err)
	}
	var got messageRow
	got.MessageSeq = row.MessageSeq
	if err := decodeMessageHeader(key, value, &got); err != nil {
		t.Fatalf("decodeMessageHeader(): %v", err)
	}
	if got.MessageID != row.MessageID || got.ClientMsgNo != row.ClientMsgNo || got.PayloadHash != row.PayloadHash || got.PayloadSize != row.PayloadSize || got.ServerTimestampMS != row.ServerTimestampMS {
		t.Fatalf("decoded row = %#v, want %#v", got, row)
	}
}

func TestMessageHeaderDirectCodecMatchesEncoder(t *testing.T) {
	row := testMessageRow()
	key := encodeMessageRowKey(ChannelKey("ch-1"), row.MessageSeq, messageHeaderFamilyID)
	want, err := encodeMessageHeader(key, row)
	if err != nil {
		t.Fatalf("encodeMessageHeader(): %v", err)
	}
	got := make([]byte, encodedMessageHeaderLen(row))
	if err := encodeMessageHeaderTo(got, key, row); err != nil {
		t.Fatalf("encodeMessageHeaderTo(): %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("encodeMessageHeaderTo() = %x, want %x", got, want)
	}
	var decoded messageRow
	decoded.MessageSeq = row.MessageSeq
	if err := decodeMessageHeader(key, got, &decoded); err != nil {
		t.Fatalf("decodeMessageHeader(): %v", err)
	}
	if decoded.MessageID != row.MessageID || decoded.ClientMsgNo != row.ClientMsgNo || decoded.PayloadHash != row.PayloadHash || decoded.PayloadSize != row.PayloadSize || decoded.ServerTimestampMS != row.ServerTimestampMS {
		t.Fatalf("decoded row = %#v, want %#v", decoded, row)
	}
}

func TestMessagePayloadCodecRoundTrip(t *testing.T) {
	row := testMessageRow()
	key := encodeMessageRowKey(ChannelKey("ch-1"), row.MessageSeq, messagePayloadFamilyID)
	value, err := encodeMessagePayload(key, row)
	if err != nil {
		t.Fatalf("encodeMessagePayload(): %v", err)
	}
	var got messageRow
	if err := decodeMessagePayload(key, value, &got); err != nil {
		t.Fatalf("decodeMessagePayload(): %v", err)
	}
	if string(got.Payload) != string(row.Payload) {
		t.Fatalf("payload = %q, want %q", got.Payload, row.Payload)
	}
}

func TestMessagePayloadDirectCodecMatchesEncoder(t *testing.T) {
	row := testMessageRow()
	key := encodeMessageRowKey(ChannelKey("ch-1"), row.MessageSeq, messagePayloadFamilyID)
	want, err := encodeMessagePayload(key, row)
	if err != nil {
		t.Fatalf("encodeMessagePayload(): %v", err)
	}
	got := make([]byte, encodedMessagePayloadLen(row))
	if err := encodeMessagePayloadTo(got, key, row); err != nil {
		t.Fatalf("encodeMessagePayloadTo(): %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("encodeMessagePayloadTo() = %x, want %x", got, want)
	}
	var decoded messageRow
	if err := decodeMessagePayload(key, got, &decoded); err != nil {
		t.Fatalf("decodeMessagePayload(): %v", err)
	}
	if string(decoded.Payload) != string(row.Payload) {
		t.Fatalf("payload = %q, want %q", decoded.Payload, row.Payload)
	}
}

func TestMessageHeaderRejectsMissingMessageID(t *testing.T) {
	row := testMessageRow()
	row.MessageID = 0
	key := encodeMessageRowKey(ChannelKey("ch-1"), row.MessageSeq, messageHeaderFamilyID)
	if _, err := encodeMessageHeader(key, row); !errors.Is(err, dberrors.ErrInvalidArgument) {
		t.Fatalf("err = %v, want invalid argument", err)
	}
}

func TestMessageHeaderDetectsChecksumMismatch(t *testing.T) {
	row := testMessageRow()
	key := encodeMessageRowKey(ChannelKey("ch-1"), row.MessageSeq, messageHeaderFamilyID)
	value, err := encodeMessageHeader(key, row)
	if err != nil {
		t.Fatalf("encodeMessageHeader(): %v", err)
	}
	value[len(value)-1] ^= 0xff
	var got messageRow
	if err := decodeMessageHeader(key, value, &got); !errors.Is(err, dberrors.ErrChecksumMismatch) {
		t.Fatalf("err = %v, want checksum mismatch", err)
	}
}

func TestLegacyMessageRowsDecodeServerTimestampMSZero(t *testing.T) {
	row := testMessageRow()
	row.Timestamp = 1234
	key := encodeMessageRowKey(ChannelKey("legacy"), row.MessageSeq, messageHeaderFamilyID)
	value, err := encodeLegacyMessageHeader(key, row)
	if err != nil {
		t.Fatalf("encodeLegacyMessageHeader(): %v", err)
	}
	var got messageRow
	got.MessageSeq = row.MessageSeq
	if err := decodeMessageHeader(key, value, &got); err != nil {
		t.Fatalf("decodeMessageHeader(): %v", err)
	}
	if got.Timestamp != 1234 {
		t.Fatalf("Timestamp = %d, want legacy 1234", got.Timestamp)
	}
	if got.ServerTimestampMS != 0 {
		t.Fatalf("ServerTimestampMS = %d, want zero for old header", got.ServerTimestampMS)
	}
}

func TestLegacyCompatibilityPayloadDecodesServerTimestampMSZero(t *testing.T) {
	row := testMessageRow()
	row.Timestamp = 5678
	payload := encodeLegacyCompatibilityPayload(row)
	got, err := decodeCompatibilityRecordPayload(payload)
	if err != nil {
		t.Fatalf("decodeCompatibilityRecordPayload(): %v", err)
	}
	if got.Timestamp != 5678 {
		t.Fatalf("Timestamp = %d, want legacy 5678", got.Timestamp)
	}
	if got.ServerTimestampMS != 0 {
		t.Fatalf("ServerTimestampMS = %d, want zero for old compatibility payload", got.ServerTimestampMS)
	}
	msg := channelMessageFromRow(got)
	if msg.Timestamp != 5678 {
		t.Fatalf("channel Timestamp = %d, want legacy 5678", msg.Timestamp)
	}
	if msg.ServerTimestampMS != 0 {
		t.Fatalf("channel ServerTimestampMS = %d, want zero for old compatibility payload", msg.ServerTimestampMS)
	}
}

func testMessageRow() messageRow {
	payload := []byte("hello")
	return messageRow{
		MessageSeq:        7,
		MessageID:         99,
		FramerFlags:       3,
		Setting:           1,
		StreamFlag:        2,
		MsgKey:            "mk",
		Expire:            30,
		ClientSeq:         11,
		ClientMsgNo:       "client-1",
		StreamNo:          "stream-1",
		StreamID:          12,
		Timestamp:         1234,
		ServerTimestampMS: 1700000000123,
		ChannelID:         "ch",
		ChannelType:       1,
		Topic:             "topic",
		FromUID:           "u1",
		Payload:           payload,
		PayloadHash:       hashPayload(payload),
		PayloadSize:       uint64(len(payload)),
	}
}

func encodeLegacyMessageHeader(key []byte, row messageRow) ([]byte, error) {
	row = normalizeMessageRow(row)
	var w rowcodec.Writer
	if err := w.Uint64(messageColumnIDMessageID, row.MessageID); err != nil {
		return nil, err
	}
	if err := w.String(messageColumnIDClientMsgNo, row.ClientMsgNo); err != nil {
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

func encodeLegacyCompatibilityPayload(row messageRow) []byte {
	row = normalizeMessageRow(row)
	payload := make([]byte, 0)
	payload = append(payload, channel.DurableMessageCodecVersion)
	payload = binary.BigEndian.AppendUint64(payload, row.MessageID)
	payload = append(payload, row.FramerFlags, row.Setting, row.StreamFlag, row.ChannelType)
	payload = binary.BigEndian.AppendUint32(payload, uint32(row.Expire))
	payload = binary.BigEndian.AppendUint64(payload, row.ClientSeq)
	payload = binary.BigEndian.AppendUint64(payload, row.StreamID)
	payload = binary.BigEndian.AppendUint32(payload, uint32(row.Timestamp))
	payload = binary.BigEndian.AppendUint64(payload, row.PayloadHash)
	payload = appendCompatibilityString(payload, row.MsgKey)
	payload = appendCompatibilityString(payload, row.ClientMsgNo)
	payload = appendCompatibilityString(payload, row.StreamNo)
	payload = appendCompatibilityString(payload, row.ChannelID)
	payload = appendCompatibilityString(payload, row.Topic)
	payload = appendCompatibilityString(payload, row.FromUID)
	return appendCompatibilityBytes(payload, row.Payload)
}
