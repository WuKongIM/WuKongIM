package cluster

import (
	"context"
	"errors"
	"testing"

	conversationusecase "github.com/WuKongIM/WuKongIM/internal/usecase/conversation"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	pkgcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	clusterchannels "github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
	"github.com/stretchr/testify/require"
)

func TestConversationStoreListsMembershipDirectoryAndHydratesOneAlignedBatch(t *testing.T) {
	node := &conversationNodeFake{
		memberships: []metadb.UserChannelMembership{
			{UID: "u1", ChannelID: "g-a", ChannelType: 2, ActivatedAt: 100},
			{UID: "u1", ChannelID: "g-b", ChannelType: 2, ActivatedAt: 90},
		},
		cursor: metadb.UserChannelMembershipCursor{ActivatedAt: 90, ChannelID: "g-b", ChannelType: 2},
		heads: []clusterchannels.ConversationHeadResult{
			{Head: clusterchannels.ConversationHead{
				LastCommittedSeq: 12, RetentionThroughSeq: 3, CurrentUserLastSendSeq: 9,
				Found: true, Message: channelruntime.Message{MessageID: 12, MessageSeq: 12, Payload: []byte("tail")},
			}},
			{Err: channelruntime.ErrNotReady},
		},
	}
	store := NewConversationStore(node)
	require.True(t, store.SupportsMembershipDirectory())
	require.False(t, (*ConversationStore)(nil).SupportsMembershipDirectory())

	rows, cursor, done, err := store.ListUserChannelMembershipPage(context.Background(), "u1", metadb.UserChannelMembershipCursor{}, 2)
	require.NoError(t, err)
	require.False(t, done)
	require.Equal(t, node.cursor, cursor)
	require.Equal(t, node.memberships, rows)
	rows[0].ChannelID = "mutated"
	require.Equal(t, "g-a", node.memberships[0].ChannelID)

	hydrated, err := store.HydrateConversationHeads(context.Background(), "u1", node.memberships)
	require.NoError(t, err)
	require.Len(t, hydrated, 2)
	require.Equal(t, conversationusecase.HydrationOK, hydrated[0].Outcome)
	require.Equal(t, uint64(12), hydrated[0].LastCommittedSeq)
	require.Equal(t, uint64(9), hydrated[0].CurrentUserLastSendSeq)
	require.Equal(t, []byte("tail"), hydrated[0].LastMessage.Payload)
	require.Equal(t, conversationusecase.HydrationRetryable, hydrated[1].Outcome)
	require.Equal(t, 1, node.headCalls)
	require.Equal(t, []channelruntime.ChannelID{{ID: "g-a", Type: 2}, {ID: "g-b", Type: 2}}, node.headIDs)
}

func TestConversationStoreMapsTerminalAndFatalHeadErrors(t *testing.T) {
	store := NewConversationStore(&conversationNodeFake{heads: []clusterchannels.ConversationHeadResult{{Err: channelruntime.ErrChannelNotFound}}})
	got, err := store.HydrateConversationHeads(context.Background(), "u1", []metadb.UserChannelMembership{{UID: "u1", ChannelID: "gone", ChannelType: 2}})
	require.NoError(t, err)
	require.Equal(t, conversationusecase.HydrationDelete, got[0].Outcome)

	wantErr := errors.New("corrupt head")
	store = NewConversationStore(&conversationNodeFake{heads: []clusterchannels.ConversationHeadResult{{Err: wantErr}}})
	_, err = store.HydrateConversationHeads(context.Background(), "u1", []metadb.UserChannelMembership{{UID: "u1", ChannelID: "g1", ChannelType: 2}})
	require.ErrorIs(t, err, wantErr)
}

func TestConversationStoreMapsTransientTransportErrorsToUnresolved(t *testing.T) {
	retryable := []error{
		channelruntime.ErrBackpressured,
		pkgcluster.ErrNotStarted,
		pkgcluster.ErrStopping,
		pkgcluster.ErrBackpressured,
		clusternet.ErrNodeNotFound,
		clusternet.ErrServiceNotFound,
		transport.ErrTimeout,
		transport.ErrNodeNotFound,
		transport.ErrQueueFull,
		transport.ErrDialFailed,
		transport.ErrBusy,
		transport.ErrStopped,
	}
	for _, transientErr := range retryable {
		t.Run(transientErr.Error(), func(t *testing.T) {
			store := NewConversationStore(&conversationNodeFake{heads: []clusterchannels.ConversationHeadResult{{Err: transientErr}}})
			got, err := store.HydrateConversationHeads(context.Background(), "u1", []metadb.UserChannelMembership{{UID: "u1", ChannelID: "g1", ChannelType: 2}})
			require.NoError(t, err)
			require.Equal(t, conversationusecase.HydrationRetryable, got[0].Outcome)
		})
	}
}

func TestConversationStoreDelegatesMembershipMutations(t *testing.T) {
	node := &conversationNodeFake{row: metadb.UserChannelMembership{UID: "u1", ChannelID: "g1", ChannelType: 2}}
	store := NewConversationStore(node)
	row, ok, err := store.GetUserChannelMembership(context.Background(), "u1", "g1", 2)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, node.row, row)
	require.NoError(t, store.AdvanceUserChannelMembershipReadSeq(context.Background(), "u1", "g1", 2, 9, 100))
	require.NoError(t, store.HideUserChannelMembership(context.Background(), "u1", "g1", 2, 12, 101))
	require.NoError(t, store.ActivateUserChannelMembership(context.Background(), "u1", "g1", 2, 102, 102))
	require.Equal(t, uint64(9), node.readSeq)
	require.Equal(t, uint64(12), node.deletedToSeq)
	require.Equal(t, int64(102), node.activatedAt)
}

type conversationNodeFake struct {
	memberships  []metadb.UserChannelMembership
	cursor       metadb.UserChannelMembershipCursor
	done         bool
	heads        []clusterchannels.ConversationHeadResult
	headCalls    int
	headIDs      []channelruntime.ChannelID
	row          metadb.UserChannelMembership
	readSeq      uint64
	deletedToSeq uint64
	activatedAt  int64
}

func (n *conversationNodeFake) ListUserChannelMembershipPage(_ context.Context, _ string, _ metadb.UserChannelMembershipCursor, _ int) ([]metadb.UserChannelMembership, metadb.UserChannelMembershipCursor, bool, error) {
	return append([]metadb.UserChannelMembership(nil), n.memberships...), n.cursor, n.done, nil
}

func (n *conversationNodeFake) ReadChannelConversationHeads(_ context.Context, ids []channelruntime.ChannelID, _ string) ([]clusterchannels.ConversationHeadResult, error) {
	n.headCalls++
	n.headIDs = append([]channelruntime.ChannelID(nil), ids...)
	return append([]clusterchannels.ConversationHeadResult(nil), n.heads...), nil
}

func (n *conversationNodeFake) GetUserChannelMembership(_ context.Context, _, _ string, _ int64) (metadb.UserChannelMembership, bool, error) {
	return n.row, n.row.UID != "", nil
}

func (n *conversationNodeFake) AdvanceUserChannelMembershipReadSeq(_ context.Context, _, _ string, _ int64, readSeq uint64, _ int64) error {
	n.readSeq = readSeq
	return nil
}

func (n *conversationNodeFake) HideUserChannelMembership(_ context.Context, _, _ string, _ int64, deletedToSeq uint64, _ int64) error {
	n.deletedToSeq = deletedToSeq
	return nil
}

func (n *conversationNodeFake) ActivateUserChannelMembership(_ context.Context, _, _ string, _ int64, activatedAt, _ int64) error {
	n.activatedAt = activatedAt
	return nil
}
