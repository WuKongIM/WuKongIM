package meta

import (
	"context"
	"errors"
	"testing"
)

func TestCompatibilityWriteBatchCommitsOneSlotFSMEnvelopeAtomically(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	const hashSlot uint16 = 50

	user := User{UID: "fsm-user", Token: "token", DeviceFlag: 1, DeviceLevel: 2}
	device := Device{UID: user.UID, DeviceFlag: 1, Token: "device-token", DeviceLevel: 3}
	channel := Channel{ChannelID: "fsm-room", ChannelType: 2, AllowStranger: 1}
	runtime := testRuntimeMeta(channel.ChannelID, channel.ChannelType)
	membership := UserChannelMembership{
		UID: user.UID, ChannelID: channel.ChannelID, ChannelType: channel.ChannelType,
		JoinSeq: 23, ActivatedAt: 800, UpdatedAt: 800,
	}
	batch := db.NewWriteBatch()
	defer batch.Close()
	if err := batch.SetSlotAppliedIndex(50, 700); err != nil {
		t.Fatalf("SetSlotAppliedIndex(): %v", err)
	}
	if err := batch.CreateUser(hashSlot, user); err != nil {
		t.Fatalf("CreateUser(): %v", err)
	}
	if err := batch.UpsertDevice(hashSlot, device); err != nil {
		t.Fatalf("UpsertDevice(): %v", err)
	}
	if err := batch.UpsertChannel(hashSlot, channel); err != nil {
		t.Fatalf("UpsertChannel(): %v", err)
	}
	if err := batch.UpsertUserChannelMembership(hashSlot, membership); err != nil {
		t.Fatalf("UpsertUserChannelMembership(): %v", err)
	}
	createdRuntime, err := batch.CreateChannelRuntimeMeta(hashSlot, runtime)
	if err != nil {
		t.Fatalf("CreateChannelRuntimeMeta(): %v", err)
	}
	added, err := batch.AddSubscribersCounted(hashSlot, channel.ChannelID, channel.ChannelType, []string{"u1", "u2", "u1"}, 7)
	if err != nil {
		t.Fatalf("AddSubscribersCounted(): %v", err)
	}
	if err := batch.Commit(); err != nil {
		t.Fatalf("Commit(): %v", err)
	}
	if createdRuntime == nil || !createdRuntime.Created {
		t.Fatalf("runtime create result = %+v, want created", createdRuntime)
	}
	if added.RequestedCount != 2 || added.ChangedCount != 2 {
		t.Fatalf("subscriber result = %+v, want requested=2 changed=2", added)
	}
	if applied, err := db.SlotAppliedIndex(ctx, 50); err != nil || applied != 700 {
		t.Fatalf("SlotAppliedIndex() = (%d, %v), want 700", applied, err)
	}
	shard := db.ForHashSlot(hashSlot)
	if got, err := shard.GetUser(ctx, user.UID); err != nil || got != user {
		t.Fatalf("GetUser() = (%+v, %v), want %+v", got, err, user)
	}
	if got, err := shard.GetDevice(ctx, device.UID, device.DeviceFlag); err != nil || got != device {
		t.Fatalf("GetDevice() = (%+v, %v), want %+v", got, err, device)
	}
	if got, err := shard.GetUserChannelMembership(ctx, membership.UID, membership.ChannelID, membership.ChannelType); err != nil || got != membership {
		t.Fatalf("GetUserChannelMembership() = (%+v, %v), want %+v", got, err, membership)
	}
	storedChannel, err := shard.GetChannel(ctx, channel.ChannelID, channel.ChannelType)
	if err != nil || storedChannel.SubscriberCount != 2 || storedChannel.SubscriberMutationVersion != 7 {
		t.Fatalf("GetChannel() = (%+v, %v), want two subscribers at version 7", storedChannel, err)
	}
	storedRuntime, err := shard.GetChannelRuntimeMeta(ctx, runtime.ChannelID, runtime.ChannelType)
	if err != nil {
		t.Fatalf("GetChannelRuntimeMeta(): %v", err)
	}

	advance := db.NewWriteBatch()
	defer advance.Close()
	if err := advance.AdvanceChannelRetentionThroughSeq(hashSlot, ChannelRetentionAdvance{
		ChannelID: runtime.ChannelID, ChannelType: runtime.ChannelType,
		ExpectedChannelEpoch: storedRuntime.ChannelEpoch,
		ExpectedLeaderEpoch:  storedRuntime.LeaderEpoch,
		ExpectedLeader:       storedRuntime.Leader,
		ExpectedLeaseUntilMS: storedRuntime.LeaseUntilMS,
		RetentionThroughSeq:  55, RetentionUpdatedAtMS: 900,
	}); err != nil {
		t.Fatalf("AdvanceChannelRetentionThroughSeq(): %v", err)
	}
	if err := advance.Commit(); err != nil {
		t.Fatalf("Commit(retention): %v", err)
	}
	storedRuntime, err = shard.GetChannelRuntimeMeta(ctx, runtime.ChannelID, runtime.ChannelType)
	if err != nil || storedRuntime.RetentionThroughSeq != 55 || storedRuntime.RetentionUpdatedAtMS != 900 {
		t.Fatalf("runtime retention = (%+v, %v)", storedRuntime, err)
	}

	remove := db.NewWriteBatch()
	defer remove.Close()
	removed, err := remove.RemoveSubscribersCounted(hashSlot, channel.ChannelID, channel.ChannelType, []string{"u1", "missing"}, 8)
	if err != nil {
		t.Fatalf("RemoveSubscribersCounted(): %v", err)
	}
	if err := remove.Commit(); err != nil {
		t.Fatalf("Commit(remove subscribers): %v", err)
	}
	if removed.RequestedCount != 2 || removed.ChangedCount != 1 {
		t.Fatalf("remove result = %+v, want requested=2 changed=1", removed)
	}

	cleanup := db.NewWriteBatch()
	defer cleanup.Close()
	if err := cleanup.DeleteUserChannelMembership(hashSlot, membership.UID, ChannelKey{ChannelID: membership.ChannelID, ChannelType: membership.ChannelType}); err != nil {
		t.Fatalf("DeleteUserChannelMembership(): %v", err)
	}
	if err := cleanup.DeleteChannelRuntimeMeta(hashSlot, runtime.ChannelID, runtime.ChannelType); err != nil {
		t.Fatalf("DeleteChannelRuntimeMeta(): %v", err)
	}
	if err := cleanup.Commit(); err != nil {
		t.Fatalf("Commit(cleanup): %v", err)
	}
	if _, err := shard.GetChannelRuntimeMeta(ctx, runtime.ChannelID, runtime.ChannelType); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetChannelRuntimeMeta(after delete) error = %v, want not found", err)
	}
	if _, err := shard.GetUserChannelMembership(ctx, membership.UID, membership.ChannelID, membership.ChannelType); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetUserChannelMembership(after delete) error = %v, want not found", err)
	}
	page, _, done, err := shard.ListUserChannelMembershipPage(ctx, membership.UID, UserChannelMembershipCursor{}, 10)
	if err != nil || !done || len(page) != 0 {
		t.Fatalf("ListUserChannelMembershipPage(after delete) = (%+v, done %v, %v), want empty", page, done, err)
	}
}

func TestCompatibilityWriteBatchRollsBackEnvelopeOnLatePreconditionFailure(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	const hashSlot uint16 = 51
	shard := db.ForHashSlot(hashSlot)
	runtime := testRuntimeMeta("guarded-room", 2)
	if err := shard.UpsertChannelRuntimeMeta(ctx, runtime); err != nil {
		t.Fatalf("UpsertChannelRuntimeMeta(): %v", err)
	}
	storedRuntime, err := shard.GetChannelRuntimeMeta(ctx, runtime.ChannelID, runtime.ChannelType)
	if err != nil {
		t.Fatalf("GetChannelRuntimeMeta(): %v", err)
	}

	batch := db.NewWriteBatch()
	defer batch.Close()
	if err := batch.UpsertUser(hashSlot, User{UID: "must-rollback", Token: "not-durable"}); err != nil {
		t.Fatalf("UpsertUser(): %v", err)
	}
	if err := batch.UpsertDevice(hashSlot, Device{UID: "must-rollback", DeviceFlag: 1, Token: "not-durable"}); err != nil {
		t.Fatalf("UpsertDevice(): %v", err)
	}
	if err := batch.AdvanceChannelRetentionThroughSeq(hashSlot, ChannelRetentionAdvance{
		ChannelID: runtime.ChannelID, ChannelType: runtime.ChannelType,
		ExpectedChannelEpoch: storedRuntime.ChannelEpoch,
		ExpectedLeaderEpoch:  storedRuntime.LeaderEpoch + 1,
		ExpectedLeader:       storedRuntime.Leader,
		ExpectedLeaseUntilMS: storedRuntime.LeaseUntilMS,
		RetentionThroughSeq:  99, RetentionUpdatedAtMS: 1000,
	}); err != nil {
		t.Fatalf("AdvanceChannelRetentionThroughSeq(stage): %v", err)
	}
	if err := batch.Commit(); !errors.Is(err, ErrStaleMeta) {
		t.Fatalf("Commit(stale runtime guard) error = %v, want stale metadata", err)
	}
	if _, err := shard.GetUser(ctx, "must-rollback"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetUser(after rollback) error = %v, want not found", err)
	}
	if _, err := shard.GetDevice(ctx, "must-rollback", 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetDevice(after rollback) error = %v, want not found", err)
	}
	unchanged, err := shard.GetChannelRuntimeMeta(ctx, runtime.ChannelID, runtime.ChannelType)
	if err != nil || unchanged.RetentionThroughSeq != storedRuntime.RetentionThroughSeq || unchanged.RouteGeneration != storedRuntime.RouteGeneration {
		t.Fatalf("runtime after rollback = (%+v, %v), want unchanged %+v", unchanged, err, storedRuntime)
	}
}
