package app

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/runtime/channelappend"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	deliveryusecase "github.com/WuKongIM/WuKongIM/internal/usecase/delivery"
	"github.com/stretchr/testify/require"
)

func TestChannelAppendAuthorityPortKeepsUnavailableResultsItemAligned(t *testing.T) {
	t.Parallel()

	items := []channelappend.SendBatchItem{
		{Command: channelappend.SendCommand{FromUID: "alice"}},
		{Command: channelappend.SendCommand{FromUID: "bob"}},
		{Command: channelappend.SendCommand{FromUID: "carol"}},
	}
	results := (channelAppendAuthorityLocal{}).SubmitForAuthority(
		context.Background(),
		channelappend.AuthorityTarget{
			ChannelID:    channelappend.ChannelID{ID: "group-1", Type: 2},
			LeaderNodeID: 1,
		},
		items,
	)

	require.Len(t, results, len(items))
	for index, result := range results {
		require.ErrorIsf(
			t,
			result.Err,
			channelappend.ErrRouteNotReady,
			"result %d must retain its aligned infrastructure failure",
			index,
		)
	}
}

func TestChannelAppendAuthorityPortWaitsForTheExactLocalBatch(t *testing.T) {
	t.Parallel()

	appender := newLifecycleBlockingChannelAppender()
	appender.release()
	group := channelappend.New(channelappend.Options{
		LocalNodeID: 1, Appender: appender,
		MessageID:           &lifecycleMessageIDAllocator{next: 100},
		AuthorityShardCount: 1, AdvancePoolSize: 1, EffectPoolSize: 1,
		AdmissionCapacityPerShard: 4, ChannelBacklogHighWatermark: 4,
		PostCommitHandoffCapacity: 4,
		InboxCoalesceWindow:       -1, InboxCoalesceMaxItems: -1,
	})
	require.NoError(t, group.Start(context.Background()))
	t.Cleanup(func() {
		require.NoError(t, group.Stop(context.Background()))
	})
	target := channelappend.AuthorityTarget{
		ChannelID:  channelappend.ChannelID{ID: "group-1", Type: 2},
		ChannelKey: "2:group-1", LeaderNodeID: 1,
	}
	items := []channelappend.SendBatchItem{
		{Command: channelappend.SendCommand{
			FromUID: "alice", ClientMsgNo: "m-1",
			ChannelID: "group-1", ChannelType: 2, ChannelKey: "2:group-1",
			Payload: []byte("first"),
		}},
		{Command: channelappend.SendCommand{
			FromUID: "alice", ClientMsgNo: "m-2",
			ChannelID: "group-1", ChannelType: 2, ChannelKey: "2:group-1",
			Payload: []byte("second"),
		}},
	}

	results := (channelAppendAuthorityLocal{group: group}).SubmitForAuthority(
		context.Background(), target, items,
	)

	require.Len(t, results, len(items))
	require.NoError(t, results[0].Err)
	require.NoError(t, results[1].Err)
	require.Equal(t, uint64(101), results[0].Result.MessageID)
	require.Equal(t, uint64(102), results[1].Result.MessageID)
	require.Equal(t, uint64(1), results[0].Result.MessageSeq)
	require.Equal(t, uint64(2), results[1].Result.MessageSeq)
}

func TestChannelAppendSubscriberPortPreservesCursorAndFiltersEmptyUIDs(t *testing.T) {
	t.Parallel()

	node := &subscriberPageNodeStub{
		uids: []string{"alice", "", "bob"},
		next: "opaque-next", done: false,
	}
	source := channelAppendSubscriberSource{node: node}
	request := channelappend.SubscriberPageRequest{
		ChannelID: channelappend.ChannelID{ID: "group-1", Type: 2},
		Cursor:    "opaque-current",
		Limit:     0,
	}

	page, err := source.NextSubscriberPage(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, "group-1", node.channelID)
	require.Equal(t, int64(2), node.channelType)
	require.Equal(t, "opaque-current", node.cursor)
	require.Equal(t, 1, node.limit, "the port must never issue an unbounded scan")
	require.Equal(t, []channelappend.Recipient{
		{UID: "alice"},
		{UID: "bob"},
	}, page.Recipients)
	require.Equal(t, "opaque-next", page.Cursor)
	require.False(t, page.Done)
}

func TestChannelAppendSubscriberPortPropagatesStorageFailureWithoutPartialPage(
	t *testing.T,
) {
	t.Parallel()

	storageErr := errors.New("subscriber slot unavailable")
	source := channelAppendSubscriberSource{node: &subscriberPageNodeStub{
		uids: []string{"must-not-escape"}, err: storageErr,
	}}

	page, err := source.NextSubscriberPage(
		context.Background(),
		channelappend.SubscriberPageRequest{
			ChannelID: channelappend.ChannelID{ID: "group-1", Type: 2},
			Limit:     50,
		},
	)
	require.ErrorIs(t, err, storageErr)
	require.Empty(t, page.Recipients)
	require.Empty(t, page.Cursor)
	require.False(t, page.Done)
}

func TestOnlineDeliveryFeedbackPortsPreserveExactSessionOwnership(t *testing.T) {
	t.Parallel()

	tracker := runtimedelivery.NewAckTracker(runtimedelivery.AckTrackerOptions{
		ShardCount: 4,
		Now:        func() int64 { return 1_788_323_400 },
	})
	runtime := runtimedelivery.NewRuntime(runtimedelivery.RuntimeOptions{
		LocalNodeID: 1,
		Acks:        tracker,
	})
	adapter := onlineDeliveryUsecaseAdapter{runtime: runtime}
	for _, pending := range []runtimedelivery.PendingRecvAck{
		{UID: "u1", SessionID: 10, MessageID: 100, MessageSeq: 1},
		{UID: "u1", SessionID: 10, MessageID: 101, MessageSeq: 2},
		{UID: "u1", SessionID: 11, MessageID: 100, MessageSeq: 3},
		{UID: "u2", SessionID: 10, MessageID: 100, MessageSeq: 4},
	} {
		require.True(t, tracker.Bind(pending))
	}

	require.NoError(t, adapter.Recvack(context.Background(), deliveryusecase.RecvackCommand{
		UID: "u1", SessionID: 10, MessageID: 100, MessageSeq: 1,
	}))
	require.Equal(t, 3, tracker.PendingCount())
	_, found := tracker.Ack(runtimedelivery.Recvack{
		UID: "u1", SessionID: 10, MessageID: 100,
	})
	require.False(t, found, "the acknowledged identity must be removed")

	require.NoError(t, adapter.SessionClosed(
		context.Background(),
		deliveryusecase.SessionClosedCommand{UID: "u1", SessionID: 10},
	))
	require.Equal(t, 2, tracker.PendingCount())
	_, found = tracker.Ack(runtimedelivery.Recvack{
		UID: "u1", SessionID: 10, MessageID: 101,
	})
	require.False(t, found, "the closed session must be removed")
	for _, ack := range []runtimedelivery.Recvack{
		{UID: "u1", SessionID: 11, MessageID: 100},
		{UID: "u2", SessionID: 10, MessageID: 100},
	} {
		_, found = tracker.Ack(ack)
		require.True(t, found, "unrelated owner session must remain pending")
	}
	require.Zero(t, tracker.PendingCount())
}

type subscriberPageNodeStub struct {
	channelID   string
	channelType int64
	cursor      string
	limit       int
	uids        []string
	next        string
	done        bool
	err         error
}

func (node *subscriberPageNodeStub) ListChannelSubscribersPage(
	_ context.Context,
	channelID string,
	channelType int64,
	cursor string,
	limit int,
) ([]string, string, bool, error) {
	node.channelID = channelID
	node.channelType = channelType
	node.cursor = cursor
	node.limit = limit
	return append([]string(nil), node.uids...), node.next, node.done, node.err
}
