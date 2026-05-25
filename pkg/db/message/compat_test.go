package message

import (
	"context"
	"encoding/binary"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestCompatEngineAppendReadAndIdempotency(t *testing.T) {
	engine, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer engine.Close()

	id := channel.ChannelID{ID: "compat", Type: 1}
	store := engine.ForChannel(channel.ChannelKey("compat:1"), id)
	msg := channel.Message{
		MessageID:   42,
		Framer:      frame.Framer{RedDot: true},
		Setting:     frame.Setting(3),
		StreamFlag:  frame.StreamFlag(2),
		MsgKey:      "msg-key",
		Expire:      60,
		ClientSeq:   7,
		ClientMsgNo: "client-1",
		StreamNo:    "stream-1",
		StreamID:    9,
		Timestamp:   100,
		ChannelID:   id.ID,
		ChannelType: id.Type,
		Topic:       "topic",
		FromUID:     "u1",
		Payload:     []byte("payload"),
	}
	payload := encodeCompatTestMessage(t, msg)

	base, err := store.Append([]channel.Record{{ID: msg.MessageID, Payload: payload, SizeBytes: len(payload)}})
	if err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if base != 0 {
		t.Fatalf("Append() base = %d, want 0", base)
	}

	got, ok, err := store.GetMessageBySeq(1)
	if err != nil || !ok {
		t.Fatalf("GetMessageBySeq() = ok %v err %v", ok, err)
	}
	if got.MessageID != msg.MessageID || got.ClientMsgNo != msg.ClientMsgNo || got.FromUID != msg.FromUID || string(got.Payload) != string(msg.Payload) {
		t.Fatalf("GetMessageBySeq() = %+v, want message fields from compat payload", got)
	}

	entry, payloadHash, ok, err := store.LookupIdempotency(channel.IdempotencyKey{
		ChannelID:   id,
		FromUID:     msg.FromUID,
		ClientMsgNo: msg.ClientMsgNo,
	})
	if err != nil || !ok {
		t.Fatalf("LookupIdempotency() = ok %v err %v", ok, err)
	}
	if entry.MessageID != msg.MessageID || entry.MessageSeq != 1 || entry.Offset != 0 {
		t.Fatalf("LookupIdempotency() entry = %+v", entry)
	}
	if payloadHash != compatTestFNV64a(msg.Payload) {
		t.Fatalf("LookupIdempotency() payloadHash = %d, want FNV %d", payloadHash, compatTestFNV64a(msg.Payload))
	}

	records, err := store.Read(0, 1<<20)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if len(records) != 1 || records[0].Index != 1 || records[0].ID != msg.MessageID {
		t.Fatalf("Read() records = %+v", records)
	}
}

func TestCompatCommittedCursorAndRetentionState(t *testing.T) {
	engine, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer engine.Close()

	store := engine.ForChannel(channel.ChannelKey("retention:1"), channel.ChannelID{ID: "retention", Type: 1})
	if err := store.StoreCommittedDispatchCursor("committed", 3); err != nil {
		t.Fatalf("StoreCommittedDispatchCursor() error = %v", err)
	}
	if err := store.AdoptRetentionBoundary(context.Background(), 5, "committed"); err != nil {
		t.Fatalf("AdoptRetentionBoundary() error = %v", err)
	}
	seq, ok, err := store.LoadCommittedDispatchCursor("committed")
	if err != nil || !ok || seq != 5 {
		t.Fatalf("LoadCommittedDispatchCursor() = seq %d ok %v err %v, want 5 true nil", seq, ok, err)
	}
	state, err := store.LoadRetentionState()
	if err != nil {
		t.Fatalf("LoadRetentionState() error = %v", err)
	}
	if state.LocalRetentionThroughSeq != 5 || state.RetainedMaxSeq != 5 {
		t.Fatalf("LoadRetentionState() = %+v, want adopted boundary", state)
	}
	keys, err := engine.ListChannelKeys()
	if err != nil {
		t.Fatalf("ListChannelKeys() error = %v", err)
	}
	if len(keys) != 1 || keys[0] != channel.ChannelKey("retention:1") {
		t.Fatalf("ListChannelKeys() = %v, want retention channel", keys)
	}
}

func encodeCompatTestMessage(t *testing.T, msg channel.Message) []byte {
	t.Helper()
	payload := make([]byte, 0, channel.DurableMessageHeaderSize+64)
	payload = append(payload, channel.DurableMessageCodecVersion)
	payload = binary.BigEndian.AppendUint64(payload, msg.MessageID)
	payload = append(payload, 0, byte(msg.Setting), byte(msg.StreamFlag), msg.ChannelType)
	payload = binary.BigEndian.AppendUint32(payload, msg.Expire)
	payload = binary.BigEndian.AppendUint64(payload, msg.ClientSeq)
	payload = binary.BigEndian.AppendUint64(payload, msg.StreamID)
	payload = binary.BigEndian.AppendUint32(payload, uint32(msg.Timestamp))
	payload = binary.BigEndian.AppendUint64(payload, compatTestFNV64a(msg.Payload))
	payload = appendCompatTestString(payload, msg.MsgKey)
	payload = appendCompatTestString(payload, msg.ClientMsgNo)
	payload = appendCompatTestString(payload, msg.StreamNo)
	payload = appendCompatTestString(payload, msg.ChannelID)
	payload = appendCompatTestString(payload, msg.Topic)
	payload = appendCompatTestString(payload, msg.FromUID)
	payload = appendCompatTestBytes(payload, msg.Payload)
	return payload
}

func appendCompatTestString(dst []byte, value string) []byte {
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(value)))
	return append(dst, value...)
}

func appendCompatTestBytes(dst []byte, value []byte) []byte {
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(value)))
	return append(dst, value...)
}

func compatTestFNV64a(payload []byte) uint64 {
	const (
		offset = 14695981039346656037
		prime  = 1099511628211
	)
	hash := uint64(offset)
	for _, b := range payload {
		hash ^= uint64(b)
		hash *= prime
	}
	return hash
}
