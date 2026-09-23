package worker

import (
	"context"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/replication"
	"github.com/stretchr/testify/require"
)

type retirementWorkerLog struct {
	replication.DurableQuorumLog
	got   ch.MQTTReplayRetirementRequest
	err   error
	panic bool
}

func (l *retirementWorkerLog) CommitMQTTReplayRetirement(_ context.Context, q ch.MQTTReplayRetirementRequest) (ch.MQTTReplayRetirementProof, error) {
	if l.panic {
		panic("retirement")
	}
	l.got = q
	return ch.MQTTReplayRetirementProof{Manifest: ch.ProposalManifest{LastOffset: 9}}, l.err
}

func TestMQTTRetirementWorkerUsesTypedAppendPool(t *testing.T) {
	log := &retirementWorkerLog{}
	request := ch.MQTTReplayRetirementRequest{MessageID: 7, ServerTimestampMS: 8}
	task := Task{Kind: TaskQuorumMQTTRetirement, Fence: ch.Fence{ChannelKey: "1:retirement", OpID: 4}, QuorumMQTTRetirement: &QuorumMQTTRetirementTask{Request: request}}
	result := task.Run(context.Background(), Deps{QuorumLog: log})
	require.NoError(t, result.Err)
	require.Equal(t, request, log.got)
	require.Equal(t, task.Fence, result.Fence)
	require.Equal(t, uint64(9), result.QuorumMQTTRetirement.Proof.Manifest.LastOffset)
	log.err = ch.ErrNotReady
	require.ErrorIs(t, task.Run(context.Background(), Deps{QuorumLog: log}).Err, ch.ErrNotReady)
	log.panic = true
	pool := &Pool{deps: Deps{QuorumLog: log}}
	failed, recovered := pool.runQueuedGroupSafely(context.Background(), []queuedTask{{task: task}})
	require.True(t, recovered)
	require.Len(t, failed, 1)
	require.Error(t, failed[0].Err)
	require.Nil(t, failed[0].QuorumMQTTRetirement)
	require.ErrorIs(t, task.Run(context.Background(), Deps{}).Err, ch.ErrInvalidConfig)
	task.QuorumMQTTRetirement = nil
	require.ErrorIs(t, task.Run(context.Background(), Deps{QuorumLog: log}).Err, ch.ErrInvalidConfig)
	pools := &Pools{StoreAppend: &Pool{}, StoreRead: &Pool{}, StoreCheckpoint: &Pool{}}
	require.Same(t, pools.StoreAppend, pools.poolFor(TaskQuorumMQTTRetirement))
}
