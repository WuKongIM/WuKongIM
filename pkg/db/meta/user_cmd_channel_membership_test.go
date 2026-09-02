package meta

import (
	"context"
	"testing"
)

func TestUserCMDChannelMembershipIsIndependentAndAckIsMonotonic(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	shard := store.db.HashSlot(15)

	row := UserCMDChannelMembership{
		UID:              "u1",
		CommandChannelID: "g1_cmd",
		ChannelType:      2,
		StartSeq:         5,
		AckSeq:           4,
		UpdatedAt:        100,
	}
	if err := shard.UpsertUserCMDChannelMembership(ctx, row); err != nil {
		t.Fatalf("UpsertUserCMDChannelMembership(): %v", err)
	}
	if err := shard.AdvanceUserCMDChannelMembershipAckSeq(ctx, row.UID, row.CommandChannelID, row.ChannelType, 9, 200); err != nil {
		t.Fatalf("AdvanceUserCMDChannelMembershipAckSeq(9): %v", err)
	}
	if err := shard.AdvanceUserCMDChannelMembershipAckSeq(ctx, row.UID, row.CommandChannelID, row.ChannelType, 7, 300); err != nil {
		t.Fatalf("AdvanceUserCMDChannelMembershipAckSeq(7): %v", err)
	}

	got, ok, err := shard.GetUserCMDChannelMembership(ctx, row.UID, row.CommandChannelID, row.ChannelType)
	if err != nil || !ok {
		t.Fatalf("GetUserCMDChannelMembership() = (%+v, %v, %v)", got, ok, err)
	}
	if got.StartSeq != 5 || got.AckSeq != 9 || got.UpdatedAt != 300 || got.Tombstone {
		t.Fatalf("CMD membership = %+v, want start=5 ack=9 updated=300 live", got)
	}
	if _, ok, err := shard.GetUserChannelMembership(ctx, row.UID, row.CommandChannelID, row.ChannelType); err != nil || ok {
		t.Fatalf("ordinary membership for CMD key = ok %v err %v, want missing", ok, err)
	}

	page, cursor, done, err := shard.ListUserCMDChannelMembershipPage(ctx, row.UID, UserCMDChannelMembershipCursor{}, 1)
	if err != nil {
		t.Fatalf("ListUserCMDChannelMembershipPage(): %v", err)
	}
	if !done || len(page) != 1 || cursor.CommandChannelID != row.CommandChannelID || cursor.ChannelType != row.ChannelType {
		t.Fatalf("CMD page = %+v cursor=%+v done=%v", page, cursor, done)
	}
}

func TestUserCMDChannelMembershipTombstoneRejectsAck(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	shard := store.db.HashSlot(16)

	row := UserCMDChannelMembership{UID: "u1", CommandChannelID: "g1_cmd", ChannelType: 2, StartSeq: 1, UpdatedAt: 100}
	if err := shard.UpsertUserCMDChannelMembership(ctx, row); err != nil {
		t.Fatalf("UpsertUserCMDChannelMembership(): %v", err)
	}
	if err := shard.TombstoneUserCMDChannelMembership(ctx, row.UID, row.CommandChannelID, row.ChannelType, 200); err != nil {
		t.Fatalf("TombstoneUserCMDChannelMembership(): %v", err)
	}
	if err := shard.AdvanceUserCMDChannelMembershipAckSeq(ctx, row.UID, row.CommandChannelID, row.ChannelType, 50, 300); err != nil {
		t.Fatalf("AdvanceUserCMDChannelMembershipAckSeq(tombstone): %v", err)
	}
	got, ok, err := shard.GetUserCMDChannelMembership(ctx, row.UID, row.CommandChannelID, row.ChannelType)
	if err != nil || !ok || !got.Tombstone || got.AckSeq != 0 || got.TombstoneAt != 200 {
		t.Fatalf("CMD tombstone after ack = (%+v, %v, %v)", got, ok, err)
	}
}
