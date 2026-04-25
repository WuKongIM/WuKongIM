package message

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/stretchr/testify/require"
)

func TestSendWithEnsuredMetaRefresherRequired(t *testing.T) {
	cluster := &fakeChannelCluster{}
	_, err := sendWithEnsuredMeta(context.Background(), 1, nil, wklog.NewNop(),
		cluster, nil, nil, channel.AppendRequest{
			ChannelID: channel.ChannelID{ID: "u2@u1", Type: frame.ChannelTypePerson},
		})
	require.ErrorIs(t, err, ErrMetaRefresherRequired)
}

func TestSendWithEnsuredMetaRefreshFails(t *testing.T) {
	refreshErr := errors.New("refresh failed")
	refresher := &fakeMetaRefresher{errs: []error{refreshErr}}
	cluster := &fakeChannelCluster{}
	_, err := sendWithEnsuredMeta(context.Background(), 1, nil, wklog.NewNop(),
		cluster, nil, refresher, channel.AppendRequest{
			ChannelID: channel.ChannelID{ID: "u2@u1", Type: frame.ChannelTypePerson},
		})
	require.ErrorIs(t, err, refreshErr)
	require.Empty(t, cluster.sendRequests)
}

func TestSendWithEnsuredMetaLocalAppend(t *testing.T) {
	refresher := &fakeMetaRefresher{
		metas: []channel.Meta{{
			Leader: 1, Epoch: 5, LeaderEpoch: 2,
		}},
	}
	cluster := &fakeChannelCluster{
		sendReplies: []fakeChannelClusterSendReply{
			{result: channel.AppendResult{MessageID: 42, MessageSeq: 3}},
		},
	}
	result, err := sendWithEnsuredMeta(context.Background(), 1, nil, wklog.NewNop(),
		cluster, nil, refresher, channel.AppendRequest{
			ChannelID: channel.ChannelID{ID: "u2@u1", Type: frame.ChannelTypePerson},
		})
	require.NoError(t, err)
	require.Equal(t, uint64(42), result.MessageID)
	require.Equal(t, uint64(3), result.MessageSeq)
	require.Len(t, cluster.sendRequests, 1)
	require.Equal(t, uint64(5), cluster.sendRequests[0].ExpectedChannelEpoch)
	require.Equal(t, uint64(2), cluster.sendRequests[0].ExpectedLeaderEpoch)
}

func TestSendWithEnsuredMetaForwardsToRemoteLeader(t *testing.T) {
	refresher := &fakeMetaRefresher{
		metas: []channel.Meta{{
			Leader: 3, Epoch: 7, LeaderEpoch: 4,
		}},
	}
	remote := &fakeRemoteAppender{
		replies: []fakeRemoteAppenderReply{
			{result: channel.AppendResult{MessageID: 99, MessageSeq: 11}},
		},
	}
	cluster := &fakeChannelCluster{}
	result, err := sendWithEnsuredMeta(context.Background(), 1, nil, wklog.NewNop(),
		cluster, remote, refresher, channel.AppendRequest{
			ChannelID: channel.ChannelID{ID: "u2@u1", Type: frame.ChannelTypePerson},
		})
	require.NoError(t, err)
	require.Equal(t, uint64(99), result.MessageID)
	require.Equal(t, uint64(11), result.MessageSeq)
	require.Empty(t, cluster.sendRequests)
	require.Len(t, remote.calls, 1)
	require.Equal(t, uint64(3), remote.calls[0].nodeID)
	require.Equal(t, uint64(7), remote.calls[0].req.ExpectedChannelEpoch)
	require.Equal(t, uint64(4), remote.calls[0].req.ExpectedLeaderEpoch)
}

func TestSendWithEnsuredMetaRemoteAppenderRequired(t *testing.T) {
	refresher := &fakeMetaRefresher{
		metas: []channel.Meta{{Leader: 3, Epoch: 7}},
	}
	cluster := &fakeChannelCluster{}
	_, err := sendWithEnsuredMeta(context.Background(), 1, nil, wklog.NewNop(),
		cluster, nil, refresher, channel.AppendRequest{
			ChannelID: channel.ChannelID{ID: "u2@u1", Type: frame.ChannelTypePerson},
		})
	require.ErrorIs(t, err, ErrRemoteAppenderRequired)
	require.Empty(t, cluster.sendRequests)
}

func TestSendWithEnsuredMetaLeaderZeroFallsToLocalAppend(t *testing.T) {
	refresher := &fakeMetaRefresher{
		metas: []channel.Meta{{Leader: 0, Epoch: 1}},
	}
	cluster := &fakeChannelCluster{
		sendReplies: []fakeChannelClusterSendReply{
			{result: channel.AppendResult{MessageID: 10, MessageSeq: 1}},
		},
	}
	result, err := sendWithEnsuredMeta(context.Background(), 1, nil, wklog.NewNop(),
		cluster, nil, refresher, channel.AppendRequest{
			ChannelID: channel.ChannelID{ID: "u2@u1", Type: frame.ChannelTypePerson},
		})
	require.NoError(t, err)
	require.Equal(t, uint64(10), result.MessageID)
	require.Len(t, cluster.sendRequests, 1)
}

type fakeRemoteAppenderReply struct {
	result channel.AppendResult
	err    error
}

type fakeRemoteAppenderCall struct {
	nodeID uint64
	req    channel.AppendRequest
}

type fakeRemoteAppender struct {
	calls   []fakeRemoteAppenderCall
	replies []fakeRemoteAppenderReply
}

func (f *fakeRemoteAppender) AppendToLeader(ctx context.Context, nodeID uint64, req channel.AppendRequest) (channel.AppendResult, error) {
	f.calls = append(f.calls, fakeRemoteAppenderCall{nodeID: nodeID, req: req})
	if len(f.replies) == 0 {
		return channel.AppendResult{}, nil
	}
	reply := f.replies[0]
	f.replies = f.replies[1:]
	return reply.result, reply.err
}
