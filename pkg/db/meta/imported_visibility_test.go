package meta

import (
	"context"
	"github.com/stretchr/testify/require"
	"io"
	"testing"
)

func TestMembershipVisibilityCodecAndSnapshotPreserveHistoryFloors(t *testing.T) {
	ctx := context.Background()
	old := UserChannelMembership{UID: "u1", ChannelID: "g1", ChannelType: 2, JoinSeq: 1, ReadSeq: 2, DeletedToSeq: 1, SourceVersion: 1}
	legacy := encodeUserChannelMembershipValue(old)
	require.Len(t, legacy, 57)
	decoded, err := decodeUserChannelMembershipValue(old.UID, old.ChannelID, old.ChannelType, legacy)
	require.NoError(t, err)
	require.Equal(t, old, decoded)
	row := old
	row.ConversationHiddenThroughSeq = 100
	encoded := encodeUserChannelMembershipValue(row)
	require.Equal(t, legacy, encoded[:len(legacy)])
	decoded, err = decodeUserChannelMembershipValue(row.UID, row.ChannelID, row.ChannelType, encoded)
	require.NoError(t, err)
	require.Equal(t, row, decoded)
	for _, bad := range [][]byte{encoded[:len(encoded)-1], append(append([]byte(nil), encoded...), 0)} {
		_, err = decodeUserChannelMembershipValue(row.UID, row.ChannelID, row.ChannelType, bad)
		require.Error(t, err)
	}
	source := openTestMetaStore(t)
	defer source.close(t)
	require.NoError(t, source.db.HashSlot(5).UpsertUserChannelMembership(ctx, row))
	reader, err := source.db.OpenHashSlotSnapshot(ctx, []uint16{5})
	require.NoError(t, err)
	body, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	target := openTestMetaStore(t)
	defer target.close(t)
	require.NoError(t, target.db.ImportHashSlotSnapshot(ctx, SlotSnapshot{HashSlots: []uint16{5}, Data: body}))
	got, ok, err := target.db.HashSlot(5).GetUserChannelMembership(ctx, row.UID, row.ChannelID, row.ChannelType)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, row, got)
	require.NoError(t, target.db.HashSlot(5).AdvanceUserChannelMembershipReadSeq(ctx, row.UID, ChannelKey{ChannelID: row.ChannelID, ChannelType: row.ChannelType}, 3, 1))
	got, ok, err = target.db.HashSlot(5).GetUserChannelMembership(ctx, row.UID, row.ChannelID, row.ChannelType)
	require.NoError(t, err)
	require.True(t, ok)
	require.EqualValues(t, 100, got.ConversationHiddenThroughSeq)
	require.EqualValues(t, 1, got.JoinSeq)
	require.EqualValues(t, 1, got.DeletedToSeq)
	newer := old
	newer.SourceVersion = 2
	require.Zero(t, resolveEnsuredUserChannelMembership(row, true, newer).ConversationHiddenThroughSeq)
	require.Equal(t, row, resolveEnsuredUserChannelMembership(row, true, old), "same generation must preserve marker")
}
