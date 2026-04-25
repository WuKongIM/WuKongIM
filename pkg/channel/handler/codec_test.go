package handler

import (
	"bytes"
	"encoding/binary"
	"reflect"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestEncodeMessageRoundTripPreservesRootDurableFields(t *testing.T) {
	msg := channel.Message{
		MessageID:   42,
		ChannelID:   "u1@u2",
		ChannelType: frame.ChannelTypePerson,
		FromUID:     "u1",
		ClientMsgNo: "m-1",
		Payload:     []byte("hello"),
	}
	setMessageField(t, &msg, "Framer", frame.Framer{NoPersist: true, RedDot: true, SyncOnce: true})
	setMessageField(t, &msg, "Setting", frame.SettingReceiptEnabled)
	setMessageField(t, &msg, "MsgKey", "k-1")
	setMessageField(t, &msg, "Expire", uint32(60))
	setMessageField(t, &msg, "ClientSeq", uint64(9))
	setMessageField(t, &msg, "StreamNo", "s-1")
	setMessageField(t, &msg, "StreamID", uint64(77))
	setMessageField(t, &msg, "StreamFlag", frame.StreamFlagEnd)
	setMessageField(t, &msg, "Timestamp", int32(123456))
	setMessageField(t, &msg, "Topic", "chat")

	encoded, err := encodeMessage(msg)
	if err != nil {
		t.Fatalf("encodeMessage() error = %v", err)
	}

	if got := binary.BigEndian.Uint64(encoded[1:9]); got != msg.MessageID {
		t.Fatalf("encoded MessageID = %d, want %d", got, msg.MessageID)
	}
	wantFlags := testEncodeFramerFlags(messageFieldValue[frame.Framer](t, msg, "Framer"))
	if got := encoded[9]; got != wantFlags {
		t.Fatalf("encoded Framer flags = %d, want %d", got, wantFlags)
	}
	if got := frame.Setting(encoded[10]); got != messageFieldValue[frame.Setting](t, msg, "Setting") {
		t.Fatalf("encoded Setting = %d, want %d", got, messageFieldValue[frame.Setting](t, msg, "Setting"))
	}
	if got := frame.StreamFlag(encoded[11]); got != messageFieldValue[frame.StreamFlag](t, msg, "StreamFlag") {
		t.Fatalf("encoded StreamFlag = %d, want %d", got, messageFieldValue[frame.StreamFlag](t, msg, "StreamFlag"))
	}
	if got := encoded[12]; got != msg.ChannelType {
		t.Fatalf("encoded ChannelType = %d, want %d", got, msg.ChannelType)
	}
	if got := binary.BigEndian.Uint32(encoded[13:17]); got != messageFieldValue[uint32](t, msg, "Expire") {
		t.Fatalf("encoded Expire = %d, want %d", got, messageFieldValue[uint32](t, msg, "Expire"))
	}
	if got := binary.BigEndian.Uint64(encoded[17:25]); got != messageFieldValue[uint64](t, msg, "ClientSeq") {
		t.Fatalf("encoded ClientSeq = %d, want %d", got, messageFieldValue[uint64](t, msg, "ClientSeq"))
	}
	if got := binary.BigEndian.Uint64(encoded[25:33]); got != messageFieldValue[uint64](t, msg, "StreamID") {
		t.Fatalf("encoded StreamID = %d, want %d", got, messageFieldValue[uint64](t, msg, "StreamID"))
	}
	if got := int32(binary.BigEndian.Uint32(encoded[33:37])); got != messageFieldValue[int32](t, msg, "Timestamp") {
		t.Fatalf("encoded Timestamp = %d, want %d", got, messageFieldValue[int32](t, msg, "Timestamp"))
	}

	view, err := decodeMessageView(encoded)
	if err != nil {
		t.Fatalf("decodeMessageView() error = %v", err)
	}
	if got := view.PayloadHash; got != hashPayload(msg.Payload) {
		t.Fatalf("PayloadHash = %d, want %d", got, hashPayload(msg.Payload))
	}

	decoded, err := decodeMessage(encoded)
	if err != nil {
		t.Fatalf("decodeMessage() error = %v", err)
	}

	if decoded.MessageID != msg.MessageID {
		t.Fatalf("MessageID = %d, want %d", decoded.MessageID, msg.MessageID)
	}
	if decoded.ChannelID != msg.ChannelID {
		t.Fatalf("ChannelID = %q, want %q", decoded.ChannelID, msg.ChannelID)
	}
	if decoded.ChannelType != msg.ChannelType {
		t.Fatalf("ChannelType = %d, want %d", decoded.ChannelType, msg.ChannelType)
	}
	if decoded.ClientMsgNo != msg.ClientMsgNo {
		t.Fatalf("ClientMsgNo = %q, want %q", decoded.ClientMsgNo, msg.ClientMsgNo)
	}
	if decoded.FromUID != msg.FromUID {
		t.Fatalf("FromUID = %q, want %q", decoded.FromUID, msg.FromUID)
	}
	if !reflect.DeepEqual(decoded.Payload, msg.Payload) {
		t.Fatalf("Payload = %q, want %q", decoded.Payload, msg.Payload)
	}
	assertMessageFieldEqual(t, decoded, "Framer", messageFieldValue[frame.Framer](t, msg, "Framer"))
	assertMessageFieldEqual(t, decoded, "Setting", messageFieldValue[frame.Setting](t, msg, "Setting"))
	assertMessageFieldEqual(t, decoded, "MsgKey", messageFieldValue[string](t, msg, "MsgKey"))
	assertMessageFieldEqual(t, decoded, "Expire", messageFieldValue[uint32](t, msg, "Expire"))
	assertMessageFieldEqual(t, decoded, "ClientSeq", messageFieldValue[uint64](t, msg, "ClientSeq"))
	assertMessageFieldEqual(t, decoded, "StreamNo", messageFieldValue[string](t, msg, "StreamNo"))
	assertMessageFieldEqual(t, decoded, "StreamID", messageFieldValue[uint64](t, msg, "StreamID"))
	assertMessageFieldEqual(t, decoded, "StreamFlag", messageFieldValue[frame.StreamFlag](t, msg, "StreamFlag"))
	assertMessageFieldEqual(t, decoded, "Timestamp", messageFieldValue[int32](t, msg, "Timestamp"))
	assertMessageFieldEqual(t, decoded, "Topic", messageFieldValue[string](t, msg, "Topic"))
}

func TestDecodeMessageRecordPreservesRootDurableFieldsAndSequence(t *testing.T) {
	msg := channel.Message{
		MessageID:   88,
		ChannelID:   "room-8",
		ChannelType: frame.ChannelTypeGroup,
		FromUID:     "alice",
		ClientMsgNo: "client-88",
		Payload:     []byte("payload-88"),
	}
	setMessageField(t, &msg, "MsgKey", "key-88")
	setMessageField(t, &msg, "ClientSeq", uint64(88))
	setMessageField(t, &msg, "StreamNo", "stream-88")
	setMessageField(t, &msg, "Timestamp", int32(8800))
	setMessageField(t, &msg, "Topic", "topic-88")

	payload, err := encodeMessage(msg)
	if err != nil {
		t.Fatalf("encodeMessage() error = %v", err)
	}

	decoded, err := decodeMessageRecord(store.LogRecord{Offset: 7, Payload: payload})
	if err != nil {
		t.Fatalf("decodeMessageRecord() error = %v", err)
	}

	if decoded.MessageSeq != 8 {
		t.Fatalf("MessageSeq = %d, want %d", decoded.MessageSeq, 8)
	}
	assertMessageFieldEqual(t, decoded, "MsgKey", "key-88")
	assertMessageFieldEqual(t, decoded, "ClientSeq", uint64(88))
	assertMessageFieldEqual(t, decoded, "StreamNo", "stream-88")
	assertMessageFieldEqual(t, decoded, "Timestamp", int32(8800))
	assertMessageFieldEqual(t, decoded, "Topic", "topic-88")
}

func TestDecodeMessagePreservesLegacyDurablePayloadLayout(t *testing.T) {
	legacy := channel.Message{
		MessageID:   109,
		ChannelID:   "legacy-room",
		ChannelType: 2,
		FromUID:     "legacy-user",
		ClientMsgNo: "legacy-client",
		Payload:     []byte("legacy-payload"),
	}

	payload := encodeLegacyCompatibleMessagePayloadForTest(t, legacy)

	decoded, err := decodeMessage(payload)
	if err != nil {
		t.Fatalf("decodeMessage() error = %v", err)
	}

	if decoded.MessageID != legacy.MessageID {
		t.Fatalf("MessageID = %d, want %d", decoded.MessageID, legacy.MessageID)
	}
	if decoded.ChannelID != legacy.ChannelID {
		t.Fatalf("ChannelID = %q, want %q", decoded.ChannelID, legacy.ChannelID)
	}
	if decoded.ChannelType != legacy.ChannelType {
		t.Fatalf("ChannelType = %d, want %d", decoded.ChannelType, legacy.ChannelType)
	}
	if decoded.ClientMsgNo != legacy.ClientMsgNo {
		t.Fatalf("ClientMsgNo = %q, want %q", decoded.ClientMsgNo, legacy.ClientMsgNo)
	}
	if decoded.FromUID != legacy.FromUID {
		t.Fatalf("FromUID = %q, want %q", decoded.FromUID, legacy.FromUID)
	}
	if !reflect.DeepEqual(decoded.Payload, legacy.Payload) {
		t.Fatalf("Payload = %q, want %q", decoded.Payload, legacy.Payload)
	}
	assertMessageFieldEqual(t, decoded, "Framer", frame.Framer{})
	assertMessageFieldEqual(t, decoded, "Setting", frame.Setting(0))
	assertMessageFieldEqual(t, decoded, "MsgKey", "")
	assertMessageFieldEqual(t, decoded, "Expire", uint32(0))
	assertMessageFieldEqual(t, decoded, "ClientSeq", uint64(0))
	assertMessageFieldEqual(t, decoded, "StreamNo", "")
	assertMessageFieldEqual(t, decoded, "StreamID", uint64(0))
	assertMessageFieldEqual(t, decoded, "StreamFlag", frame.StreamFlag(0))
	assertMessageFieldEqual(t, decoded, "Timestamp", int32(0))
	assertMessageFieldEqual(t, decoded, "Topic", "")
}

func setMessageField(t *testing.T, msg *channel.Message, name string, value any) {
	t.Helper()

	field := reflect.ValueOf(msg).Elem().FieldByName(name)
	if !field.IsValid() {
		t.Fatalf("Message is missing %s", name)
	}
	field.Set(reflect.ValueOf(value))
}

func messageFieldValue[T any](t *testing.T, msg channel.Message, name string) T {
	t.Helper()

	field := reflect.ValueOf(msg).FieldByName(name)
	if !field.IsValid() {
		t.Fatalf("Message is missing %s", name)
	}
	value, ok := field.Interface().(T)
	if !ok {
		t.Fatalf("Message.%s has unexpected type %T", name, field.Interface())
	}
	return value
}

func assertMessageFieldEqual(t *testing.T, msg channel.Message, name string, want any) {
	t.Helper()

	field := reflect.ValueOf(msg).FieldByName(name)
	if !field.IsValid() {
		t.Fatalf("Message is missing %s", name)
	}
	got := field.Interface()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Message.%s = %#v, want %#v", name, got, want)
	}
}

func testEncodeFramerFlags(framer frame.Framer) uint8 {
	var flags uint8
	if framer.NoPersist {
		flags |= 1 << 0
	}
	if framer.RedDot {
		flags |= 1 << 1
	}
	if framer.SyncOnce {
		flags |= 1 << 2
	}
	if framer.DUP {
		flags |= 1 << 3
	}
	if framer.HasServerVersion {
		flags |= 1 << 4
	}
	if framer.End {
		flags |= 1 << 5
	}
	return flags
}

func encodeLegacyCompatibleMessagePayloadForTest(t *testing.T, msg channel.Message) []byte {
	t.Helper()

	var buf bytes.Buffer
	if err := buf.WriteByte(channel.DurableMessageCodecVersion); err != nil {
		t.Fatalf("WriteByte() error = %v", err)
	}
	if err := binary.Write(&buf, binary.BigEndian, msg.MessageID); err != nil {
		t.Fatalf("binary.Write(MessageID) error = %v", err)
	}
	if _, err := buf.Write(make([]byte, channel.DurableMessageHeaderSize-9)); err != nil {
		t.Fatalf("Write(header padding) error = %v", err)
	}
	binary.BigEndian.PutUint64(buf.Bytes()[37:45], hashPayload(msg.Payload))
	if err := writeString(&buf, ""); err != nil {
		t.Fatalf("writeString(msgKey) error = %v", err)
	}
	if err := writeString(&buf, msg.ClientMsgNo); err != nil {
		t.Fatalf("writeString(clientMsgNo) error = %v", err)
	}
	if err := writeString(&buf, ""); err != nil {
		t.Fatalf("writeString(streamNo) error = %v", err)
	}
	if err := writeString(&buf, msg.ChannelID); err != nil {
		t.Fatalf("writeString(channelID) error = %v", err)
	}
	if err := writeString(&buf, ""); err != nil {
		t.Fatalf("writeString(topic) error = %v", err)
	}
	if err := writeString(&buf, msg.FromUID); err != nil {
		t.Fatalf("writeString(fromUID) error = %v", err)
	}
	if err := writeBytes(&buf, msg.Payload); err != nil {
		t.Fatalf("writeBytes(payload) error = %v", err)
	}
	payload := buf.Bytes()
	payload[12] = msg.ChannelType
	return payload
}
