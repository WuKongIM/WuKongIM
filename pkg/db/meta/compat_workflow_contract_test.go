package meta

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"testing"
)

func TestCompatibilityFacadePersistsChannelServingState(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("Close(): %v", err)
		}
	}()
	ctx := context.Background()
	shard := db.ForSlot(7)
	if shard.HashSlot() != 7 {
		t.Fatalf("HashSlot() = %d, want 7", shard.HashSlot())
	}
	shards := db.ForHashSlots([]uint16{7, 9})
	if len(shards) != 2 || shards[0].HashSlot() != 7 || shards[1].HashSlot() != 9 {
		t.Fatalf("ForHashSlots() = %#v, want slots 7 and 9", shards)
	}

	firstUser := User{UID: "alice", Token: "token-v1", DeviceFlag: 1, DeviceLevel: 2}
	if err := shard.CreateUser(ctx, firstUser); err != nil {
		t.Fatalf("CreateUser(): %v", err)
	}
	if err := shard.CreateUser(ctx, firstUser); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("CreateUser(duplicate) error = %v, want already exists", err)
	}
	firstUser.Token = "token-v2"
	if err := shard.UpdateUser(ctx, firstUser); err != nil {
		t.Fatalf("UpdateUser(): %v", err)
	}
	if err := shard.UpsertUser(ctx, User{UID: "bobby", Token: "bob-token"}); err != nil {
		t.Fatalf("UpsertUser(): %v", err)
	}
	gotUser, err := shard.GetUser(ctx, "alice")
	if err != nil || gotUser != firstUser {
		t.Fatalf("GetUser() = (%+v, %v), want %+v", gotUser, err, firstUser)
	}
	users, cursor, done, err := shard.ListUsersPage(ctx, UserCursor{}, 1)
	if err != nil || len(users) != 1 || users[0].UID != "alice" || done || cursor.UID != "alice" {
		t.Fatalf("ListUsersPage(first) = (%+v, %+v, %v, %v)", users, cursor, done, err)
	}
	users, _, _, err = shard.ListUsersPage(ctx, cursor, 10)
	if err != nil || len(users) != 1 || users[0].UID != "bobby" {
		t.Fatalf("ListUsersPage(resume) = (%+v, %v), want bobby", users, err)
	}

	if err := shard.UpsertDevice(ctx, Device{UID: "alice", DeviceFlag: 1, Token: "device-token", DeviceLevel: 3}); err != nil {
		t.Fatalf("UpsertDevice(): %v", err)
	}
	device, err := shard.GetDevice(ctx, "alice", 1)
	if err != nil || device.Token != "device-token" || device.DeviceLevel != 3 {
		t.Fatalf("GetDevice() = (%+v, %v)", device, err)
	}

	room := Channel{ChannelID: "room", ChannelType: 2, AllowStranger: 1}
	if err := shard.CreateChannel(ctx, room); err != nil {
		t.Fatalf("CreateChannel(): %v", err)
	}
	room.Ban = 1
	if err := shard.UpdateChannel(ctx, room); err != nil {
		t.Fatalf("UpdateChannel(): %v", err)
	}
	if err := shard.UpsertChannel(ctx, Channel{ChannelID: "room", ChannelType: 3, Large: 1}); err != nil {
		t.Fatalf("UpsertChannel(second type): %v", err)
	}
	gotRoom, err := shard.GetChannel(ctx, room.ChannelID, room.ChannelType)
	if err != nil || gotRoom.Ban != 1 || gotRoom.AllowStranger != 1 {
		t.Fatalf("GetChannel() = (%+v, %v)", gotRoom, err)
	}
	byID, err := shard.ListChannelsByChannelID(ctx, "room")
	if err != nil || len(byID) != 2 || byID[0].ChannelType != 2 || byID[1].ChannelType != 3 {
		t.Fatalf("ListChannelsByChannelID() = (%+v, %v)", byID, err)
	}
	channels, channelCursor, done, err := shard.ListChannelsPage(ctx, ChannelCursor{}, 1)
	if err != nil || len(channels) != 1 || done || channelCursor.ChannelID == "" {
		t.Fatalf("ListChannelsPage(first) = (%+v, %+v, %v, %v)", channels, channelCursor, done, err)
	}
	channels, _, _, err = shard.ListChannelsPage(ctx, channelCursor, 10)
	if err != nil || len(channels) != 1 {
		t.Fatalf("ListChannelsPage(resume) = (%+v, %v), want one channel", channels, err)
	}

	if err := shard.AddSubscribers(ctx, "room", 2, []string{"bobby", "alice", "alice"}, 4); err != nil {
		t.Fatalf("AddSubscribers(): %v", err)
	}
	present, err := shard.ContainsSubscriber(ctx, "room", 2, "alice")
	if err != nil || !present {
		t.Fatalf("ContainsSubscriber() = (%v, %v), want true", present, err)
	}
	present, err = shard.HasSubscribers(ctx, "room", 2)
	if err != nil || !present {
		t.Fatalf("HasSubscribers() = (%v, %v), want true", present, err)
	}
	subscribers, subscriberCursor, done, err := shard.ListSubscribersPage(ctx, "room", 2, "", 1)
	if err != nil || len(subscribers) != 1 || subscribers[0] != "alice" || done || subscriberCursor != "alice" {
		t.Fatalf("ListSubscribersPage(first) = (%v, %q, %v, %v)", subscribers, subscriberCursor, done, err)
	}
	snapshot, err := shard.ListSubscribersSnapshot(ctx, "room", 2)
	if err != nil || !reflect.DeepEqual(snapshot, []string{"alice", "bobby"}) {
		t.Fatalf("ListSubscribersSnapshot() = (%v, %v)", snapshot, err)
	}
	if err := shard.RemoveSubscribers(ctx, "room", 2, []string{"alice"}, 5); err != nil {
		t.Fatalf("RemoveSubscribers(): %v", err)
	}
	present, err = shard.ContainsSubscriber(ctx, "room", 2, "alice")
	if err != nil || present {
		t.Fatalf("ContainsSubscriber(after remove) = (%v, %v), want false", present, err)
	}

	runtimeMeta := ChannelRuntimeMeta{
		ChannelID: "room", ChannelType: 2,
		ChannelEpoch: 1, LeaderEpoch: 2, Leader: 1,
		Replicas: []uint64{1, 2}, ISR: []uint64{1, 2}, MinISR: 1,
		LeaseUntilMS: 100, RouteGeneration: 1,
	}
	if err := shard.UpsertChannelRuntimeMeta(ctx, runtimeMeta); err != nil {
		t.Fatalf("UpsertChannelRuntimeMeta(): %v", err)
	}
	storedRuntime, err := shard.GetChannelRuntimeMeta(ctx, "room", 2)
	if err != nil {
		t.Fatalf("GetChannelRuntimeMeta(): %v", err)
	}
	if err := shard.AdvanceChannelRetentionThroughSeq(ctx, ChannelRetentionAdvance{
		ChannelID: "room", ChannelType: 2,
		ExpectedChannelEpoch: storedRuntime.ChannelEpoch,
		ExpectedLeaderEpoch:  storedRuntime.LeaderEpoch,
		ExpectedLeader:       storedRuntime.Leader,
		ExpectedLeaseUntilMS: storedRuntime.LeaseUntilMS,
		RetentionThroughSeq:  88,
		RetentionUpdatedAtMS: 123,
	}); err != nil {
		t.Fatalf("AdvanceChannelRetentionThroughSeq(): %v", err)
	}
	storedRuntime, err = shard.GetChannelRuntimeMeta(ctx, "room", 2)
	if err != nil || storedRuntime.RetentionThroughSeq != 88 || storedRuntime.RetentionUpdatedAtMS != 123 {
		t.Fatalf("runtime retention = (%+v, %v)", storedRuntime, err)
	}
	runtimePage, _, _, err := shard.ListChannelRuntimeMetaPage(ctx, ChannelRuntimeMetaCursor{}, 10)
	if err != nil || len(runtimePage) != 1 || runtimePage[0].ChannelID != "room" {
		t.Fatalf("ListChannelRuntimeMetaPage() = (%+v, %v)", runtimePage, err)
	}
	allRuntime, err := db.ListChannelRuntimeMeta(ctx)
	if err != nil || len(allRuntime) != 1 || allRuntime[0].ChannelID != "room" {
		t.Fatalf("ListChannelRuntimeMeta() = (%+v, %v)", allRuntime, err)
	}
	if err := shard.DeleteChannelRuntimeMeta(ctx, "room", 2); err != nil {
		t.Fatalf("DeleteChannelRuntimeMeta(): %v", err)
	}
	if _, err := shard.GetChannelRuntimeMeta(ctx, "room", 2); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetChannelRuntimeMeta(after delete) error = %v, want not found", err)
	}

	if err := shard.DeleteUser(ctx, "bobby"); err != nil {
		t.Fatalf("DeleteUser(): %v", err)
	}
	if _, err := shard.GetUser(ctx, "bobby"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetUser(after delete) error = %v, want not found", err)
	}
}

func TestCompatibilityWriteBatchPublishesUIDOwnedProjectionsAtomically(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("Close(): %v", err)
		}
	}()
	ctx := context.Background()
	const hashSlot uint16 = 12
	shard := db.ForHashSlot(hashSlot)

	ordinary := UserChannelMembership{
		UID: "alice", ChannelID: "room", ChannelType: 2,
		JoinSeq: 5, ActivatedAt: 50, SourceVersion: 3, UpdatedAt: 50,
	}
	cmd := UserCMDChannelMembership{
		UID: "alice", CommandChannelID: "room_cmd", ChannelType: 2,
		StartSeq: 6, UpdatedAt: 60,
	}
	batch := db.NewWriteBatch()
	defer batch.Close()
	if err := batch.SetSlotAppliedIndex(44, 900); err != nil {
		t.Fatalf("SetSlotAppliedIndex(): %v", err)
	}
	if err := batch.UpsertUserChannelMembership(hashSlot, ordinary); err != nil {
		t.Fatalf("UpsertUserChannelMembership(): %v", err)
	}
	if err := batch.AdvanceUserChannelMembershipReadSeq(hashSlot, ordinary.UID, ChannelKey{ChannelID: ordinary.ChannelID, ChannelType: ordinary.ChannelType}, 8, 70); err != nil {
		t.Fatalf("AdvanceUserChannelMembershipReadSeq(): %v", err)
	}
	if err := batch.UpsertUserCMDChannelMembership(hashSlot, cmd); err != nil {
		t.Fatalf("UpsertUserCMDChannelMembership(): %v", err)
	}
	if err := batch.AdvanceUserCMDChannelMembershipAckSeq(hashSlot, UserCMDChannelMembership{
		UID: cmd.UID, CommandChannelID: cmd.CommandChannelID, ChannelType: cmd.ChannelType,
		AckSeq: 9, UpdatedAt: 80,
	}); err != nil {
		t.Fatalf("AdvanceUserCMDChannelMembershipAckSeq(): %v", err)
	}
	if err := batch.BindPluginUser(hashSlot, PluginUserBinding{UID: "alice", PluginNo: "assistant", CreatedAtMS: 10, UpdatedAtMS: 11}); err != nil {
		t.Fatalf("BindPluginUser(): %v", err)
	}
	if err := batch.UpsertChannelLatest(hashSlot, ChannelLatest{
		ChannelID: "room", ChannelType: 2,
		LastMessageID: 700, LastMessageSeq: 12, LastAt: 90,
		FromUID: "alice", Payload: []byte("latest"), UpdatedAt: 91,
	}); err != nil {
		t.Fatalf("UpsertChannelLatest(): %v", err)
	}
	eventResult, err := batch.AppendMessageEvent(hashSlot, MessageEventAppend{
		ChannelID: "room", ChannelType: 2, ClientMsgNo: "client-1",
		EventID: "event-1", EventType: EventTypeStreamDelta,
		Payload:    []byte(`{"kind":"text","delta":"hello"}`),
		OccurredAt: 100, UpdatedAt: 101,
	})
	if err != nil {
		t.Fatalf("AppendMessageEvent(): %v", err)
	}
	if err := batch.Commit(); err != nil {
		t.Fatalf("Commit(): %v", err)
	}
	if eventResult.MsgEventSeq != 1 || eventResult.State.LastEventID != "event-1" {
		t.Fatalf("event result after commit = %+v", eventResult)
	}
	if applied, err := db.SlotAppliedIndex(ctx, 44); err != nil || applied != 900 {
		t.Fatalf("SlotAppliedIndex() = (%d, %v), want 900", applied, err)
	}

	gotOrdinary, err := shard.GetUserChannelMembership(ctx, ordinary.UID, ordinary.ChannelID, ordinary.ChannelType)
	if err != nil || gotOrdinary.ReadSeq != 8 || gotOrdinary.SourceVersion != 3 {
		t.Fatalf("GetUserChannelMembership() = (%+v, %v)", gotOrdinary, err)
	}
	ordinaryPage, _, _, err := shard.ListUserChannelMembershipPage(ctx, ordinary.UID, UserChannelMembershipCursor{}, 10)
	if err != nil || len(ordinaryPage) != 1 || ordinaryPage[0].ChannelID != ordinary.ChannelID {
		t.Fatalf("ListUserChannelMembershipPage() = (%+v, %v)", ordinaryPage, err)
	}
	gotCMD, ok, err := shard.GetUserCMDChannelMembership(ctx, cmd.UID, cmd.CommandChannelID, cmd.ChannelType)
	if err != nil || !ok || gotCMD.StartSeq != 6 || gotCMD.AckSeq != 9 || gotCMD.UpdatedAt != 80 {
		t.Fatalf("GetUserCMDChannelMembership() = (%+v, %v, %v)", gotCMD, ok, err)
	}
	cmdPage, _, _, err := shard.ListUserCMDChannelMembershipPage(ctx, cmd.UID, UserCMDChannelMembershipCursor{}, 10)
	if err != nil || len(cmdPage) != 1 || cmdPage[0].CommandChannelID != cmd.CommandChannelID {
		t.Fatalf("ListUserCMDChannelMembershipPage() = (%+v, %v)", cmdPage, err)
	}
	latest, err := shard.GetChannelLatest(ctx, "room", 2)
	if err != nil || latest.LastMessageSeq != 12 || !bytes.Equal(latest.Payload, []byte("latest")) {
		t.Fatalf("GetChannelLatest() = (%+v, %v)", latest, err)
	}
	state, err := shard.GetMessageEventState(ctx, "room", 2, "client-1", EventKeyDefault)
	if err != nil || state.LastMsgEventSeq != 1 || state.LastEventID != "event-1" {
		t.Fatalf("GetMessageEventState() = (%+v, %v)", state, err)
	}
	states, err := shard.ListMessageEventStates(ctx, "room", 2, "client-1", 10)
	if err != nil || len(states) != 1 || states[0].LastEventID != "event-1" {
		t.Fatalf("ListMessageEventStates() = (%+v, %v)", states, err)
	}
	bindings, err := shard.ListPluginBindingsByUID(ctx, "alice")
	if err != nil || len(bindings) != 1 || bindings[0].PluginNo != "assistant" {
		t.Fatalf("ListPluginBindingsByUID() = (%+v, %v)", bindings, err)
	}
	present, err := shard.ExistPluginBindingByUID(ctx, "alice")
	if err != nil || !present {
		t.Fatalf("ExistPluginBindingByUID() = (%v, %v), want true", present, err)
	}
	scanned, pluginCursor, more, err := shard.ScanPluginBindingsByPluginNo(ctx, "assistant", PluginUserBindingCursor{}, 1)
	if err != nil || len(scanned) != 1 || more || pluginCursor.UID != "alice" {
		t.Fatalf("ScanPluginBindingsByPluginNo() = (%+v, %+v, %v, %v)", scanned, pluginCursor, more, err)
	}

	tombstone := db.NewWriteBatch()
	defer tombstone.Close()
	if err := tombstone.TombstoneUserCMDChannelMembership(hashSlot, UserCMDChannelMembership{
		UID: cmd.UID, CommandChannelID: cmd.CommandChannelID, ChannelType: cmd.ChannelType,
		TombstoneAt: 120, UpdatedAt: 120,
	}); err != nil {
		t.Fatalf("TombstoneUserCMDChannelMembership(): %v", err)
	}
	if err := tombstone.Commit(); err != nil {
		t.Fatalf("Commit(tombstone): %v", err)
	}
	gotCMD, ok, err = shard.GetUserCMDChannelMembership(ctx, cmd.UID, cmd.CommandChannelID, cmd.ChannelType)
	if err != nil || !ok || !gotCMD.Tombstone || gotCMD.TombstoneAt != 120 {
		t.Fatalf("CMD membership after tombstone = (%+v, %v, %v)", gotCMD, ok, err)
	}

	rebind := db.NewWriteBatch()
	defer rebind.Close()
	if err := rebind.UpsertUserCMDChannelMembership(hashSlot, UserCMDChannelMembership{
		UID: cmd.UID, CommandChannelID: cmd.CommandChannelID, ChannelType: cmd.ChannelType,
		StartSeq: 40, AckSeq: 39, UpdatedAt: 130,
	}); err != nil {
		t.Fatalf("UpsertUserCMDChannelMembership(rebind): %v", err)
	}
	if err := rebind.Commit(); err != nil {
		t.Fatalf("Commit(rebind): %v", err)
	}
	gotCMD, ok, err = shard.GetUserCMDChannelMembership(ctx, cmd.UID, cmd.CommandChannelID, cmd.ChannelType)
	if err != nil || !ok || gotCMD.Tombstone || gotCMD.StartSeq != 40 || gotCMD.AckSeq != 39 {
		t.Fatalf("CMD membership after rebind = (%+v, %v, %v)", gotCMD, ok, err)
	}

	if err := shard.UnbindPluginUser(ctx, "alice", "assistant"); err != nil {
		t.Fatalf("UnbindPluginUser(): %v", err)
	}
	present, err = shard.ExistPluginBindingByUID(ctx, "alice")
	if err != nil || present {
		t.Fatalf("ExistPluginBindingByUID(after unbind) = (%v, %v), want false", present, err)
	}
	if err := shard.DeleteUserChannelMembership(ctx, ordinary.UID, ChannelKey{ChannelID: ordinary.ChannelID, ChannelType: ordinary.ChannelType}); err != nil {
		t.Fatalf("DeleteUserChannelMembership(): %v", err)
	}
	if _, err := shard.GetUserChannelMembership(ctx, ordinary.UID, ordinary.ChannelID, ordinary.ChannelType); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetUserChannelMembership(after delete) error = %v, want not found", err)
	}
}

func TestCompatibilitySnapshotFacadeRoundTripsPinnedHashSlotState(t *testing.T) {
	source, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open(source): %v", err)
	}
	defer source.Close()
	target, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open(target): %v", err)
	}
	defer target.Close()
	ctx := context.Background()
	const hashSlot uint16 = 21

	if err := source.ForHashSlot(hashSlot).CreateUser(ctx, User{UID: "snapshot-user", Token: "before"}); err != nil {
		t.Fatalf("CreateUser(): %v", err)
	}
	present, err := source.HasBackupBusinessData(ctx, []uint16{hashSlot})
	if err != nil || !present {
		t.Fatalf("HasBackupBusinessData() = (%v, %v), want true", present, err)
	}
	snapshot, err := source.ExportHashSlotSnapshot(ctx, []uint16{hashSlot})
	if err != nil || snapshot.Stats.EntryCount == 0 {
		t.Fatalf("ExportHashSlotSnapshot() = (%+v, %v)", snapshot, err)
	}
	if err := source.ForHashSlot(hashSlot).UpsertUser(ctx, User{UID: "snapshot-user", Token: "after"}); err != nil {
		t.Fatalf("UpsertUser(after export): %v", err)
	}
	if err := target.ImportHashSlotSnapshot(ctx, snapshot); err != nil {
		t.Fatalf("ImportHashSlotSnapshot(): %v", err)
	}
	restored, err := target.ForHashSlot(hashSlot).GetUser(ctx, "snapshot-user")
	if err != nil || restored.Token != "before" {
		t.Fatalf("GetUser(restored) = (%+v, %v), want pinned token before", restored, err)
	}

	stream, err := source.OpenBackupHashSlotSnapshot(ctx, []uint16{hashSlot})
	if err != nil {
		t.Fatalf("OpenBackupHashSlotSnapshot(): %v", err)
	}
	payload, err := io.ReadAll(stream)
	if err != nil {
		t.Fatalf("ReadAll(backup stream): %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close(backup stream): %v", err)
	}
	stats, err := target.ImportHashSlotSnapshotReaderForRestoreWithStats(
		ctx, []uint16{hashSlot}, bytes.NewReader(payload), int64(len(payload)), true,
	)
	if err != nil || stats.EntryCount == 0 {
		t.Fatalf("ImportHashSlotSnapshotReaderForRestoreWithStats() = (%+v, %v)", stats, err)
	}
	restored, err = target.ForHashSlot(hashSlot).GetUser(ctx, "snapshot-user")
	if err != nil || restored.Token != "" {
		t.Fatalf("GetUser(after secure restore) = (%+v, %v), want invalidated token", restored, err)
	}

	fullStream, err := source.OpenHashSlotSnapshot(ctx, []uint16{hashSlot})
	if err != nil {
		t.Fatalf("OpenHashSlotSnapshot(): %v", err)
	}
	fullPayload, err := io.ReadAll(fullStream)
	if err != nil {
		t.Fatalf("ReadAll(full stream): %v", err)
	}
	if err := fullStream.Close(); err != nil {
		t.Fatalf("Close(full stream): %v", err)
	}
	if err := target.MetaDB().ImportHashSlotSnapshotReader(ctx, []uint16{hashSlot}, bytes.NewReader(fullPayload), int64(len(fullPayload))); err != nil {
		t.Fatalf("ImportHashSlotSnapshotReader(): %v", err)
	}
	restored, err = target.ForHashSlot(hashSlot).GetUser(ctx, "snapshot-user")
	if err != nil || restored.Token != "after" {
		t.Fatalf("GetUser(after full stream import) = (%+v, %v), want token after", restored, err)
	}
	if err := target.DeleteHashSlotData(ctx, hashSlot); err != nil {
		t.Fatalf("DeleteHashSlotData(): %v", err)
	}
	if _, err := target.ForHashSlot(hashSlot).GetUser(ctx, "snapshot-user"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetUser(after DeleteHashSlotData) error = %v, want not found", err)
	}
}
