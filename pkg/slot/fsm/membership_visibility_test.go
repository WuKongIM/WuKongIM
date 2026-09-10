package fsm

import (
	"context"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestSlotFSMRetainsImportedConversationVisibility(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm, err := NewStateMachineWithHashSlots(db, 11, []uint16{5})
	require.NoError(t, err)
	row := metadb.UserChannelMembership{UID: "u1", ChannelID: "g1", ChannelType: 2, JoinSeq: 1, ConversationHiddenThroughSeq: 100, SourceVersion: 1}
	command := EncodeUpsertUserChannelMembershipsCommand([]metadb.UserChannelMembership{row})
	_, err = sm.Apply(ctx, multiraft.Command{SlotID: 11, HashSlot: 5, Index: 1, Term: 1, Data: command})
	require.NoError(t, err)
	got, err := db.ForHashSlot(5).GetUserChannelMembership(ctx, row.UID, row.ChannelID, row.ChannelType)
	require.NoError(t, err)
	require.Equal(t, row, got)
	old := row
	old.ConversationHiddenThroughSeq = 0
	decoded, err := decodeUserChannelMembershipEntry(encodeUserChannelMembershipEntry(old, true), true)
	require.NoError(t, err)
	require.Equal(t, old, decoded)
}
