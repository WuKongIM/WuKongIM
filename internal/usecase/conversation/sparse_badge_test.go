package conversation

import (
	"context"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestSparseBadgeRetainsActualLegacyReadBoundary(t *testing.T) {
	row := metadb.UserChannelMembership{ChannelID: "room", ChannelType: 2, JoinSeq: 1, ReadSeq: 1}
	head := HydrationResult{ReadThroughSeq: 11, NonBusinessUnread: 7, LastMessage: &LastMessage{MessageSeq: 10}}
	item, ok := conversationFromMembership(row, head)
	require.True(t, ok)
	require.Equal(t, uint64(3), item.Unread)
	require.Equal(t, uint64(1), legacyEffectiveReadSeq(item), "sequence minus count would skip two unread ordinary messages")
}

func TestSetUnreadUsesLeaderSelectedOrdinaryBoundary(t *testing.T) {
	store := newConversationMutationStore()
	store.head.ReadThroughSeq = 11
	store.head.UnreadBoundary = 4
	store.head.BoundaryComputed = true
	app := New(Options{Hydrator: store, MembershipMutations: store})
	require.NoError(t, app.SetUnread(context.Background(), SetUnreadCommand{UID: "u1", ChannelID: "g1", ChannelType: 2, Unread: 2}))
	require.Len(t, store.readMutations, 1)
	require.Equal(t, uint64(4), store.readMutations[0].readSeq, "must retain ordinary messages at 7 and 10")
}

func TestRecoveryBarrierDoesNotResurfaceDeletedConversation(t *testing.T) {
	row := metadb.UserChannelMembership{ChannelID: "room", ChannelType: 2, JoinSeq: 1, DeletedToSeq: 10}
	head := HydrationResult{ReadThroughSeq: 11, NonBusinessUnread: 1, LastMessage: &LastMessage{MessageSeq: 10}}
	_, ok := conversationFromMembership(row, head)
	require.False(t, ok)
	row.ActivatedAt = 1
	item, ok := conversationFromMembership(row, head)
	require.True(t, ok)
	require.Nil(t, item.LastMessage)
	require.Zero(t, item.Unread)
}
