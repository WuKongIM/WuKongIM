package meta

import (
	"context"
	"testing"
)

func TestMessageEventBatchReplayReturnsOriginalDurableProjection(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	const hashSlot HashSlot = 70
	event := MessageEventAppend{
		ChannelID: "event-room", ChannelType: 2, ClientMsgNo: "client-1",
		EventID: "event-1", EventType: EventTypeStreamDelta,
		Payload:    []byte(`{"kind":"text","delta":"first"}`),
		OccurredAt: 100, UpdatedAt: 101,
	}

	batch := store.db.NewBatch()
	defer batch.Close()
	first, err := batch.AppendMessageEvent(hashSlot, event)
	if err != nil {
		t.Fatalf("AppendMessageEvent(first): %v", err)
	}
	replayInput := event
	replayInput.Payload = []byte(`{"kind":"text","delta":"must-not-apply"}`)
	replayed, err := batch.AppendMessageEvent(hashSlot, replayInput)
	if err != nil {
		t.Fatalf("AppendMessageEvent(same batch replay): %v", err)
	}
	if replayed.MsgEventSeq != first.MsgEventSeq || replayed.State.LastEventID != event.EventID || string(replayed.State.SnapshotPayload) != string(first.State.SnapshotPayload) {
		t.Fatalf("same-batch replay = %+v, want original %+v", replayed, first)
	}
	if err := batch.Commit(ctx); err != nil {
		t.Fatalf("Commit(): %v", err)
	}

	persistedReplay := store.db.NewBatch()
	defer persistedReplay.Close()
	fromDisk, err := persistedReplay.AppendMessageEvent(hashSlot, replayInput)
	if err != nil {
		t.Fatalf("AppendMessageEvent(persisted replay): %v", err)
	}
	if fromDisk.MsgEventSeq != first.MsgEventSeq || fromDisk.State.LastEventID != event.EventID || string(fromDisk.State.SnapshotPayload) != string(first.State.SnapshotPayload) {
		t.Fatalf("persisted replay = %+v, want original %+v", fromDisk, first)
	}
	if err := persistedReplay.Commit(ctx); err != nil {
		t.Fatalf("Commit(persisted replay): %v", err)
	}
}

func TestUserCMDMembershipUpsertPreservesLiveBindingAndTombstoneBoundaries(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	shard := store.db.HashSlot(71)
	row := UserCMDChannelMembership{
		UID: "alice", CommandChannelID: "room_cmd", ChannelType: 2,
		StartSeq: 10, AckSeq: 5, UpdatedAt: 100,
	}
	if err := shard.UpsertUserCMDChannelMembership(ctx, row); err != nil {
		t.Fatalf("UpsertUserCMDChannelMembership(create): %v", err)
	}
	if err := shard.UpsertUserCMDChannelMembership(ctx, UserCMDChannelMembership{
		UID: row.UID, CommandChannelID: row.CommandChannelID, ChannelType: row.ChannelType,
		StartSeq: 99, AckSeq: 8, UpdatedAt: 200,
	}); err != nil {
		t.Fatalf("UpsertUserCMDChannelMembership(live replay): %v", err)
	}
	got, ok, err := shard.GetUserCMDChannelMembership(ctx, row.UID, row.CommandChannelID, row.ChannelType)
	if err != nil || !ok || got.StartSeq != 10 || got.AckSeq != 8 || got.UpdatedAt != 200 {
		t.Fatalf("live membership = (%+v, %v, %v), want preserved start and monotonic ack", got, ok, err)
	}
	if err := shard.TombstoneUserCMDChannelMembership(ctx, row.UID, row.CommandChannelID, row.ChannelType, 300); err != nil {
		t.Fatalf("TombstoneUserCMDChannelMembership(): %v", err)
	}
	if err := shard.UpsertUserCMDChannelMembership(ctx, UserCMDChannelMembership{
		UID: row.UID, CommandChannelID: row.CommandChannelID, ChannelType: row.ChannelType,
		StartSeq: 500, AckSeq: 500, Tombstone: true, TombstoneAt: 400, UpdatedAt: 400,
	}); err != nil {
		t.Fatalf("UpsertUserCMDChannelMembership(tombstone replay): %v", err)
	}
	got, ok, err = shard.GetUserCMDChannelMembership(ctx, row.UID, row.CommandChannelID, row.ChannelType)
	if err != nil || !ok || !got.Tombstone || got.TombstoneAt != 300 || got.StartSeq != 10 || got.AckSeq != 8 {
		t.Fatalf("tombstone replay changed durable boundary: (%+v, %v, %v)", got, ok, err)
	}
}
