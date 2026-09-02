package message

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
)

func TestCompatibilityReadIndexesRetentionAndCursorWorkflow(t *testing.T) {
	engine := openCompatEngine(t)
	key := channel.ChannelKey("compat-operational")
	id := channel.ChannelID{ID: "compat-operational", Type: 1}
	store := mustForChannel(t, engine, key, id)
	defer store.Close()
	records := []channel.Record{
		compatOperationalRecord(t, id, 1001, "shared", "u1", 100),
		compatOperationalRecord(t, id, 1002, "shared", "u2", 200),
		compatOperationalRecord(t, id, 1003, "third", "u1", 300),
	}
	if _, err := store.Append(records); err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	forward, err := store.ReadOffsets(0, 2, 1<<20)
	if err != nil || len(forward) != 2 || forward[0].Offset != 0 || forward[1].Offset != 1 {
		t.Fatalf("ReadOffsets() = %#v, %v", forward, err)
	}
	reverse, err := store.ReadOffsetsReverse(math.MaxUint64, 2, 1<<20)
	if err != nil || len(reverse) != 2 || reverse[0].Offset != 2 || reverse[1].Offset != 1 {
		t.Fatalf("ReadOffsetsReverse() = %#v, %v", reverse, err)
	}
	engineForward, err := engine.Read(key, 0, 2, 1<<20)
	if err != nil || len(engineForward) != 2 || engineForward[0].Offset != 0 || engineForward[1].Offset != 1 {
		t.Fatalf("Engine.Read() = %#v, %v", engineForward, err)
	}
	engineReverse, err := engine.ReadReverse(key, math.MaxUint64, 2, 1<<20)
	if err != nil || len(engineReverse) != 2 || engineReverse[0].Offset != 2 || engineReverse[1].Offset != 1 {
		t.Fatalf("Engine.ReadReverse() = %#v, %v", engineReverse, err)
	}
	latest, err := engine.ListLatestMessages(context.Background(), 0, 2)
	if err != nil || len(latest.Messages) != 2 || latest.Messages[0].MessageID != 1003 || latest.Messages[1].MessageID != 1002 || !latest.HasMore {
		t.Fatalf("Engine.ListLatestMessages() = %#v, %v", latest, err)
	}

	message, present, err := store.GetMessageByMessageID(1002)
	if err != nil || !present || message.MessageSeq != 2 || message.FromUID != "u2" {
		t.Fatalf("GetMessageByMessageID() = %#v, present %v, error %v", message, present, err)
	}
	seq, present, err := store.GetLastSenderMessageSeq(context.Background(), "u1", 3)
	if err != nil || !present || seq != 3 {
		t.Fatalf("GetLastSenderMessageSeq() = %d, present %v, error %v", seq, present, err)
	}
	clientPage, nextBefore, hasMore, err := store.ListMessagesByClientMsgNo("shared", 0, 1)
	if err != nil || len(clientPage) != 1 || clientPage[0].MessageSeq != 2 || !hasMore || nextBefore != 2 {
		t.Fatalf("ListMessagesByClientMsgNo(first) = %#v, cursor %d, more %v, error %v", clientPage, nextBefore, hasMore, err)
	}
	clientPage, _, hasMore, err = store.ListMessagesByClientMsgNo("shared", nextBefore, 1)
	if err != nil || len(clientPage) != 1 || clientPage[0].MessageSeq != 1 || hasMore {
		t.Fatalf("ListMessagesByClientMsgNo(second) = %#v, more %v, error %v", clientPage, hasMore, err)
	}

	idempotencyKey := channel.IdempotencyKey{ChannelID: id, FromUID: "u3", ClientMsgNo: "manual"}
	idempotencyEntry := channel.IdempotencyEntry{MessageID: 1999, MessageSeq: 4, Offset: 3}
	if err := store.PutIdempotency(idempotencyKey, idempotencyEntry); err != nil {
		t.Fatalf("PutIdempotency() error = %v", err)
	}
	gotEntry, present, err := store.GetIdempotency(idempotencyKey)
	if err != nil || !present || gotEntry != idempotencyEntry {
		t.Fatalf("GetIdempotency() = %#v, present %v, error %v", gotEntry, present, err)
	}

	expired, err := store.ScanExpiredMessagePrefix(0, time.Unix(250, 0), 10)
	if err != nil || expired.FromSeq != 1 || expired.ThroughSeq != 2 || expired.Count != 2 {
		t.Fatalf("ScanExpiredMessagePrefix() = %#v, %v", expired, err)
	}
	if err := store.StoreCommittedDispatchCursor("delivery", 2); err != nil {
		t.Fatalf("StoreCommittedDispatchCursor() error = %v", err)
	}
	if err := store.StoreCommittedDispatchCursor("delivery", 1); err != nil {
		t.Fatalf("StoreCommittedDispatchCursor(regression no-op) error = %v", err)
	}
	if durable, err := store.ConfirmCommittedDispatchCursorDurable("delivery", 2); err != nil || durable != 2 {
		t.Fatalf("ConfirmCommittedDispatchCursorDurable() = %d, %v", durable, err)
	}
	if _, err := store.ConfirmCommittedDispatchCursorDurable("delivery", 3); !errors.Is(err, channel.ErrCorruptState) {
		t.Fatalf("ConfirmCommittedDispatchCursorDurable(ahead) error = %v", err)
	}
	if err := store.AdvanceCommittedDispatchCursorDurable("delivery", 3); err != nil {
		t.Fatalf("AdvanceCommittedDispatchCursorDurable() error = %v", err)
	}
	if err := store.AdvanceCommittedDispatchCursorDurable("delivery", 2); !errors.Is(err, channel.ErrCorruptState) {
		t.Fatalf("AdvanceCommittedDispatchCursorDurable(regression) error = %v", err)
	}
	if cursor, present, err := store.LoadCommittedDispatchCursor("delivery"); err != nil || !present || cursor != 3 {
		t.Fatalf("LoadCommittedDispatchCursor() = %d, present %v, error %v", cursor, present, err)
	}
}

func TestCompatibilityCheckpointHistoryAndSnapshotWorkflow(t *testing.T) {
	engine := openCompatEngine(t)
	id := channel.ChannelID{ID: "compat-state", Type: 1}
	store := mustForChannel(t, engine, "compat-state", id)
	defer store.Close()
	if _, err := store.Append([]channel.Record{
		compatOperationalRecord(t, id, 2001, "state-1", "u1", 100),
		compatOperationalRecord(t, id, 2002, "state-2", "u1", 200),
		compatOperationalRecord(t, id, 2003, "state-3", "u1", 300),
	}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if err := store.StoreCheckpoint(channel.Checkpoint{Epoch: 1, LogStartOffset: 0, HW: 1}); err != nil {
		t.Fatalf("StoreCheckpoint() error = %v", err)
	}
	if err := store.StoreCheckpointHWMonotonic(context.Background(), 2); err != nil {
		t.Fatalf("StoreCheckpointHWMonotonic() error = %v", err)
	}
	if err := store.StoreCheckpointHWMonotonic(context.Background(), 1); err != nil {
		t.Fatalf("StoreCheckpointHWMonotonic(no-op) error = %v", err)
	}
	if err := store.StoreCheckpointMonotonic(context.Background(), channel.Checkpoint{Epoch: 1, LogStartOffset: 0, HW: 3}, 3, 3); err != nil {
		t.Fatalf("StoreCheckpointMonotonic() error = %v", err)
	}
	checkpoint, err := store.LoadCheckpoint()
	if err != nil || checkpoint != (channel.Checkpoint{Epoch: 1, HW: 3}) {
		t.Fatalf("LoadCheckpoint() = %#v, %v", checkpoint, err)
	}

	point := channel.EpochPoint{Epoch: 2, StartOffset: 3}
	if err := store.BeginEpoch(context.Background(), point, 3); err != nil {
		t.Fatalf("BeginEpoch() error = %v", err)
	}
	if err := store.BeginEpoch(context.Background(), point, 3); err != nil {
		t.Fatalf("BeginEpoch(idempotent) error = %v", err)
	}
	if err := store.AppendHistory(channel.EpochPoint{Epoch: 3, StartOffset: 4}); err != nil {
		t.Fatalf("AppendHistory() error = %v", err)
	}
	history, err := store.LoadHistory()
	if err != nil || len(history) != 2 || history[0] != point || history[1] != (channel.EpochPoint{Epoch: 3, StartOffset: 4}) {
		t.Fatalf("LoadHistory() = %#v, %v", history, err)
	}
	if err := store.TruncateHistoryTo(3); err != nil {
		t.Fatalf("TruncateHistoryTo() error = %v", err)
	}
	history, err = store.LoadHistory()
	if err != nil || len(history) != 1 || history[0] != point {
		t.Fatalf("LoadHistory(after truncate) = %#v, %v", history, err)
	}

	if err := store.StoreSnapshotPayload([]byte("compat-snapshot-before")); err != nil {
		t.Fatalf("StoreSnapshotPayload() error = %v", err)
	}
	if payload, err := store.LoadSnapshotPayload(); err != nil || string(payload) != "compat-snapshot-before" {
		t.Fatalf("LoadSnapshotPayload() = %q, %v", payload, err)
	}
	leo, err := store.InstallSnapshotAtomically(context.Background(), channel.Snapshot{
		ChannelKey: "compat-state", Epoch: 2, EndOffset: 3, Payload: []byte("compat-snapshot-after"),
	}, channel.Checkpoint{Epoch: 2, LogStartOffset: 3, HW: 3}, point)
	if err != nil || leo != 3 {
		t.Fatalf("InstallSnapshotAtomically() = %d, %v", leo, err)
	}
	if payload, err := store.LoadSnapshotPayload(); err != nil || string(payload) != "compat-snapshot-after" {
		t.Fatalf("LoadSnapshotPayload(after install) = %q, %v", payload, err)
	}
	checkpoint, err = store.LoadCheckpoint()
	if err != nil || checkpoint != (channel.Checkpoint{Epoch: 2, LogStartOffset: 3, HW: 3}) {
		t.Fatalf("snapshot checkpoint = %#v, %v", checkpoint, err)
	}
}

func compatOperationalRecord(t *testing.T, id channel.ChannelID, messageID uint64, clientMsgNo, fromUID string, timestamp int32) channel.Record {
	t.Helper()
	message := channel.Message{
		MessageID: messageID, ClientMsgNo: clientMsgNo, FromUID: fromUID, Timestamp: timestamp,
		ChannelID: id.ID, ChannelType: id.Type, Payload: []byte(clientMsgNo),
	}
	payload := encodeCompatTestMessage(t, message)
	return channel.Record{ID: messageID, Payload: payload, SizeBytes: len(payload)}
}
