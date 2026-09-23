package worker

import (
	"context"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/replication"
	"github.com/stretchr/testify/require"
)

type anchorWorkerLog struct {
	replication.DurableQuorumLog
	calls int
	got   ch.MQTTReplayAnchorRequest
	err   error
}

func (l *anchorWorkerLog) CommitMQTTReplayAnchor(_ context.Context, q ch.MQTTReplayAnchorRequest) (ch.MQTTReplayAnchorProof, error) {
	l.calls++
	l.got = q
	return ch.MQTTReplayAnchorProof{Manifest: ch.ProposalManifest{LastOffset: 9}}, l.err
}
func TestMQTTAnchorWorkerUsesTypedAppendPool(t *testing.T) {
	log := &anchorWorkerLog{}
	request := ch.MQTTReplayAnchorRequest{MessageID: 7, ServerTimestampMS: 8}
	task := Task{Kind: TaskQuorumMQTTAnchor, Fence: ch.Fence{ChannelKey: "1:anchor", OpID: 4}, QuorumMQTTAnchor: &QuorumMQTTAnchorTask{Request: request}}
	result := task.Run(context.Background(), Deps{QuorumLog: log})
	require.NoError(t, result.Err)
	require.Equal(t, 1, log.calls)
	require.Equal(t, request, log.got)
	require.Equal(t, task.Fence, result.Fence)
	require.Equal(t, uint64(9), result.QuorumMQTTAnchor.Proof.Manifest.LastOffset)
	log.err = ch.ErrNotReady
	require.ErrorIs(t, task.Run(context.Background(), Deps{QuorumLog: log}).Err, ch.ErrNotReady)
	task.QuorumMQTTAnchor = nil
	require.ErrorIs(t, task.Run(context.Background(), Deps{QuorumLog: log}).Err, ch.ErrInvalidConfig)
	pools := &Pools{StoreAppend: &Pool{}, StoreRead: &Pool{}, StoreCheckpoint: &Pool{}}
	require.Same(t, pools.StoreAppend, pools.poolFor(TaskQuorumMQTTAnchor))
}
