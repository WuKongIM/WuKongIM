package fsm

import (
	"context"
	"encoding/binary"
	"fmt"
	"strings"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

// Failure cases: wrong-owner rows must roll back the entire command; replay
// must filter the migrated shard; batching must retain source-version and
// personal-state semantics; malformed or oversized wire input must be rejected.
func TestUpsertMembershipBatchOwnershipAndMigration(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm, err := NewStateMachineWithHashSlots(db, 11, []uint16{5})
	require.NoError(t, err)
	items := []UserChannelMembershipBatchItem{
		{HashSlot: 5, Membership: metadb.UserChannelMembership{UID: "u5", ChannelID: "g", ChannelType: 2, JoinSeq: 10, SourceVersion: 1}},
		{HashSlot: 7, Membership: metadb.UserChannelMembership{UID: "u7", ChannelID: "g", ChannelType: 2, JoinSeq: 10, SourceVersion: 1}},
	}
	data, err := EncodeUpsertUserChannelMembershipBatchCommandChecked(items)
	require.NoError(t, err)
	commands := []multiraft.Command{
		{SlotID: 11, HashSlot: 5, Index: 1, Term: 1, Data: EncodeUpsertUserCommand(metadb.User{UID: "rollback"})},
		{SlotID: 11, HashSlot: 5, Index: 2, Term: 1, Data: data},
	}
	_, err = sm.(multiraft.BatchStateMachine).ApplyBatch(ctx, commands)
	require.ErrorIs(t, err, metadb.ErrInvalidArgument)
	_, err = db.ForHashSlot(5).GetUser(ctx, "rollback")
	require.ErrorIs(t, err, metadb.ErrNotFound)
	for _, item := range items {
		_, err = db.ForHashSlot(item.HashSlot).GetUserChannelMembership(ctx, item.Membership.UID, "g", 2)
		require.ErrorIs(t, err, metadb.ErrNotFound)
	}
	index, err := sm.(multiraft.DurableAppliedStateMachine).DurableAppliedIndex(ctx)
	require.NoError(t, err)
	require.Zero(t, index)
	sm.(*stateMachine).UpdateOwnedHashSlots([]uint16{5, 7})
	_, err = sm.(multiraft.BatchStateMachine).ApplyBatch(ctx, commands)
	require.NoError(t, err)
	for _, item := range items {
		got, err := db.ForHashSlot(item.HashSlot).GetUserChannelMembership(ctx, item.Membership.UID, "g", 2)
		require.NoError(t, err)
		require.Equal(t, item.Membership, got)
	}

	// Replay the original multi-shard command into only the migrated shard.
	targetDB := openTestDB(t)
	target, err := NewStateMachineWithHashSlots(targetDB, 21, []uint16{7})
	require.NoError(t, err)
	_, err = target.Apply(ctx, multiraft.Command{SlotID: 21, HashSlot: 7, Index: 1, Term: 1,
		Data: EncodeApplyDeltaCommand(11, 2, 7, data)})
	require.NoError(t, err)
	_, err = targetDB.ForHashSlot(5).GetUserChannelMembership(ctx, "u5", "g", 2)
	require.ErrorIs(t, err, metadb.ErrNotFound)
	got, err := targetDB.ForHashSlot(7).GetUserChannelMembership(ctx, "u7", "g", 2)
	require.NoError(t, err)
	require.Equal(t, items[1].Membership, got)
}

func TestUpsertMembershipBatchPreservesVersionsAndPersonalState(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm, err := NewStateMachineWithHashSlots(db, 11, []uint16{5, 7})
	require.NoError(t, err)
	var index uint64
	apply := func(row metadb.UserChannelMembership) metadb.UserChannelMembership {
		t.Helper()
		data, err := EncodeUpsertUserChannelMembershipBatchCommandChecked([]UserChannelMembershipBatchItem{{HashSlot: 5, Membership: row}})
		require.NoError(t, err)
		index++
		_, err = sm.Apply(ctx, multiraft.Command{SlotID: 11, HashSlot: 5, Index: index, Term: 1, Data: data})
		require.NoError(t, err)
		got, err := db.ForHashSlot(5).GetUserChannelMembership(ctx, row.UID, row.ChannelID, row.ChannelType)
		require.NoError(t, err)
		return got
	}
	original := metadb.UserChannelMembership{UID: "u", ChannelID: "g", ChannelType: 2,
		JoinSeq: 2, ReadSeq: 20, DeletedToSeq: 15, ConversationHiddenThroughSeq: 30,
		ActivatedAt: 100, SourceVersion: 4, UpdatedAt: 100}
	require.Equal(t, original, apply(original))
	projection := metadb.UserChannelMembership{UID: "u", ChannelID: "g", ChannelType: 2,
		JoinSeq: 31, ReadSeq: 30, DeletedToSeq: 30, SourceVersion: 5, UpdatedAt: 101}
	preserved := original
	preserved.SourceVersion, preserved.UpdatedAt = 5, 101
	require.Equal(t, preserved, apply(projection))
	removed := projection
	removed.SourceVersion, removed.Tombstone, removed.TombstoneAt = 6, true, 102
	removed.UpdatedAt = 102
	tombstone := apply(removed)
	require.True(t, tombstone.Tombstone)
	require.EqualValues(t, 6, tombstone.SourceVersion)
	require.Equal(t, tombstone, apply(projection), "stale projection cannot resurrect membership")
	projection.SourceVersion, projection.UpdatedAt = 7, 103
	require.Equal(t, projection, apply(projection), "rejoin resets visibility from captured tail")
}

func TestUpsertMembershipBatchCodecBoundsAndCanonicalOrder(t *testing.T) {
	row := func(uid string, slot uint16) UserChannelMembershipBatchItem {
		return UserChannelMembershipBatchItem{HashSlot: slot, Membership: metadb.UserChannelMembership{UID: uid, ChannelID: "g", ChannelType: 2}}
	}
	a, b := row("a", 5), row("b", 7)
	data, err := EncodeUpsertUserChannelMembershipBatchCommandChecked([]UserChannelMembershipBatchItem{b, a})
	require.NoError(t, err)
	canonical, err := EncodeUpsertUserChannelMembershipBatchCommandChecked([]UserChannelMembershipBatchItem{a, b})
	require.NoError(t, err)
	require.Equal(t, canonical, data)
	hashes, err := DecodeCommandHashSlots(data, 5)
	require.NoError(t, err)
	require.Equal(t, []uint16{5, 7}, hashes)
	forge := func(items []UserChannelMembershipBatchItem) []byte {
		buf := []byte{commandVersion, 69}
		for _, item := range items {
			entry := make([]byte, 2)
			binary.BigEndian.PutUint16(entry, item.HashSlot)
			entry = append(entry, encodeUserChannelMembershipEntry(item.Membership, true)...)
			buf = appendBytesTLVField(buf, tagPersonDirectoryTaskBatchEntry, entry)
		}
		return buf
	}
	items := make([]UserChannelMembershipBatchItem, 129)
	for i := range items {
		items[i] = row(fmt.Sprintf("u%03d", i), 5)
	}
	_, err = EncodeUpsertUserChannelMembershipBatchCommandChecked(items[:128])
	require.NoError(t, err)
	largeUID := row(strings.Repeat("u", 65537), 5)
	largeChannel := a
	largeChannel.Membership.ChannelID = strings.Repeat("g", 256<<10)
	for name, input := range map[string][]UserChannelMembershipBatchItem{
		"empty": nil, "duplicate": {a, a}, "row overflow": items,
		"uid bytes": {largeUID}, "command bytes": {largeChannel},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := EncodeUpsertUserChannelMembershipBatchCommandChecked(input)
			require.Error(t, err)
			_, err = decodeCommand(forge(input))
			require.Error(t, err)
		})
	}
	for _, malformed := range [][]byte{forge([]UserChannelMembershipBatchItem{b, a}), data[:len(data)-1], append(append([]byte{}, data...), 255)} {
		_, err = decodeCommand(malformed)
		require.Error(t, err)
	}
}
