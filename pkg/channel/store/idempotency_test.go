package store

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
)

func TestChannelStorePersistsIdempotencyAndRestoresSnapshotAtOffset(t *testing.T) {
	engine := openTestEngine(t)
	key := channel.ChannelKey("channel/1/u1")
	id := channel.ChannelID{ID: "u1", Type: 1}
	st := engine.ForChannel(key, id)

	firstKey := channel.IdempotencyKey{
		ChannelID:   id,
		FromUID:     "s1",
		ClientMsgNo: "m1",
	}
	firstEntry := channel.IdempotencyEntry{
		MessageID:  9,
		MessageSeq: 3,
		Offset:     2,
	}
	if err := st.PutIdempotency(firstKey, firstEntry); err != nil {
		t.Fatalf("PutIdempotency(first) error = %v", err)
	}

	secondKey := channel.IdempotencyKey{
		ChannelID:   id,
		FromUID:     "s1",
		ClientMsgNo: "m2",
	}
	secondEntry := channel.IdempotencyEntry{
		MessageID:  10,
		MessageSeq: 5,
		Offset:     4,
	}
	if err := st.PutIdempotency(secondKey, secondEntry); err != nil {
		t.Fatalf("PutIdempotency(second) error = %v", err)
	}

	got, ok, err := st.GetIdempotency(firstKey)
	if err != nil {
		t.Fatalf("GetIdempotency() error = %v", err)
	}
	if !ok {
		t.Fatal("expected idempotency entry")
	}
	if got != firstEntry {
		t.Fatalf("idempotency entry = %+v, want %+v", got, firstEntry)
	}

	snapshot, err := st.SnapshotIdempotency(3)
	if err != nil {
		t.Fatalf("SnapshotIdempotency() error = %v", err)
	}

	restoreEngine := openTestEngine(t)
	restored := restoreEngine.ForChannel(key, id)
	if err := restored.RestoreIdempotency(snapshot); err != nil {
		t.Fatalf("RestoreIdempotency() error = %v", err)
	}

	got, ok, err = restored.GetIdempotency(firstKey)
	if err != nil {
		t.Fatalf("restored GetIdempotency(first) error = %v", err)
	}
	if !ok {
		t.Fatal("expected restored first entry")
	}
	if got != firstEntry {
		t.Fatalf("restored first entry = %+v, want %+v", got, firstEntry)
	}

	_, ok, err = restored.GetIdempotency(secondKey)
	if err != nil {
		t.Fatalf("restored GetIdempotency(second) error = %v", err)
	}
	if ok {
		t.Fatal("expected second entry to be excluded by snapshot offset")
	}
}

func TestChannelStoreRoundTripsFullIdempotencyStateAtCurrentOffset(t *testing.T) {
	st := openTestChannelStore(t, channel.ChannelKey("channel/1/u1"), channel.ChannelID{ID: "u1", Type: 1})
	id := channel.ChannelID{ID: "u1", Type: 1}

	firstKey := channel.IdempotencyKey{
		ChannelID:   id,
		FromUID:     "s1",
		ClientMsgNo: "m1",
	}
	firstEntry := channel.IdempotencyEntry{
		MessageID:  9,
		MessageSeq: 3,
		Offset:     2,
	}
	if err := st.PutIdempotency(firstKey, firstEntry); err != nil {
		t.Fatalf("PutIdempotency(first) error = %v", err)
	}

	secondKey := channel.IdempotencyKey{
		ChannelID:   id,
		FromUID:     "s1",
		ClientMsgNo: "m2",
	}
	secondEntry := channel.IdempotencyEntry{
		MessageID:  10,
		MessageSeq: 5,
		Offset:     4,
	}
	if err := st.PutIdempotency(secondKey, secondEntry); err != nil {
		t.Fatalf("PutIdempotency(second) error = %v", err)
	}

	snapshot, err := st.SnapshotIdempotency(5)
	if err != nil {
		t.Fatalf("SnapshotIdempotency() error = %v", err)
	}

	restored := openTestChannelStore(t, channel.ChannelKey("channel/1/u1"), id)
	if err := restored.RestoreIdempotency(snapshot); err != nil {
		t.Fatalf("RestoreIdempotency() error = %v", err)
	}

	got, ok, err := restored.GetIdempotency(firstKey)
	if err != nil {
		t.Fatalf("GetIdempotency(first) error = %v", err)
	}
	if !ok {
		t.Fatal("expected restored first entry")
	}
	if got != firstEntry {
		t.Fatalf("restored first entry = %+v, want %+v", got, firstEntry)
	}

	got, ok, err = restored.GetIdempotency(secondKey)
	if err != nil {
		t.Fatalf("GetIdempotency(second) error = %v", err)
	}
	if !ok {
		t.Fatal("expected restored second entry")
	}
	if got != secondEntry {
		t.Fatalf("restored second entry = %+v, want %+v", got, secondEntry)
	}
}

func TestGetIdempotencyUsesUniqueIndexWithoutRawLogReplay(t *testing.T) {
	st := openTestChannelStore(t, channel.ChannelKey("channel/1/u1"), channel.ChannelID{ID: "u1", Type: 1})
	id := channel.ChannelID{ID: "u1", Type: 1}

	payload := mustEncodeStoreMessage(t, channel.Message{
		MessageID:   31,
		ClientMsgNo: "m1",
		FromUID:     "sender-1",
		ChannelID:   id.ID,
		ChannelType: id.Type,
		Payload:     []byte("one"),
	})
	_, err := st.Append([]channel.Record{{Payload: payload, SizeBytes: len(payload)}})
	if err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	got, ok, err := st.GetIdempotency(channel.IdempotencyKey{
		ChannelID:   id,
		FromUID:     "sender-1",
		ClientMsgNo: "m1",
	})
	if err != nil {
		t.Fatalf("GetIdempotency() error = %v", err)
	}
	if !ok {
		t.Fatal("expected idempotency entry from structured unique index")
	}
	want := channel.IdempotencyEntry{MessageID: 31, MessageSeq: 1, Offset: 0}
	if got != want {
		t.Fatalf("idempotency entry = %+v, want %+v", got, want)
	}
}
