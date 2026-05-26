package message

import (
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
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
	if got.MessageID != row.MessageID || got.ClientMsgNo != row.ClientMsgNo || got.PayloadHash != row.PayloadHash || got.PayloadSize != row.PayloadSize {
		t.Fatalf("decoded row = %#v, want %#v", got, row)
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

func testMessageRow() messageRow {
	payload := []byte("hello")
	return messageRow{
		MessageSeq:  7,
		MessageID:   99,
		FramerFlags: 3,
		Setting:     1,
		StreamFlag:  2,
		MsgKey:      "mk",
		Expire:      30,
		ClientSeq:   11,
		ClientMsgNo: "client-1",
		StreamNo:    "stream-1",
		StreamID:    12,
		Timestamp:   1234,
		ChannelID:   "ch",
		ChannelType: 1,
		Topic:       "topic",
		FromUID:     "u1",
		Payload:     payload,
		PayloadHash: hashPayload(payload),
		PayloadSize: uint64(len(payload)),
	}
}
