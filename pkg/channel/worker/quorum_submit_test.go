package worker

import (
	"context"
	"sync"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/replication"
	"github.com/stretchr/testify/require"
)

// asyncQuorumLog records submitted commits and completes them only on demand.
type asyncQuorumLog struct {
	captureDurableQuorumLog
	mu        sync.Mutex
	submitted chan replication.Proposal
	pending   []func(replication.Receipt, error)
	ctxs      []context.Context
	syncCalls int
}

func (l *asyncQuorumLog) Commit(ctx context.Context, p replication.Proposal) (replication.Receipt, error) {
	l.mu.Lock()
	l.syncCalls++
	l.mu.Unlock()
	return replication.Receipt{}, nil
}

func (l *asyncQuorumLog) SubmitCommit(ctx context.Context, p replication.Proposal, done func(replication.Receipt, error)) error {
	l.mu.Lock()
	l.pending = append(l.pending, done)
	l.ctxs = append(l.ctxs, ctx)
	l.mu.Unlock()
	// Completion after cancellation mirrors the quorum round terminal callback.
	context.AfterFunc(ctx, func() { done(replication.Receipt{}, ctx.Err()) })
	l.submitted <- p
	return nil
}

func (l *asyncQuorumLog) complete(i int, r replication.Receipt) {
	l.mu.Lock()
	done := l.pending[i]
	l.mu.Unlock()
	done(r, nil)
}

func quorumCommitTask(n byte) Task {
	fence := ch.Fence{ChannelKey: ch.ChannelKey("1:q"), Generation: 1, Epoch: 1, LeaderEpoch: 1, OpID: ch.OpID(n)}
	return Task{Kind: TaskQuorumCommit, Fence: fence, QuorumCommit: &QuorumCommitTask{Proposal: replication.Proposal{CommandID: ch.CommandID{31: n}}}}
}

func TestPoolQuorumSubmitReleasesWorkerButRetainsBudget(t *testing.T) {
	log := &asyncQuorumLog{submitted: make(chan replication.Proposal, 4)}
	sink := &captureSink{ch: make(chan Result, 4)}
	pool, err := NewPool(PoolConfig{Name: "q", Workers: 1, QueueSize: 1}, Deps{QuorumLog: log}, sink)
	require.NoError(t, err)
	defer pool.Close()

	require.NoError(t, pool.Submit(context.Background(), quorumCommitTask(1)))
	<-log.submitted
	// The single worker is free again while the first proposal is unresolved.
	require.NoError(t, pool.Submit(context.Background(), quorumCommitTask(2)))
	<-log.submitted
	require.ErrorIs(t, pool.Submit(context.Background(), quorumCommitTask(3)), ch.ErrBackpressured)
	require.Zero(t, sink.Len())

	log.complete(0, replication.Receipt{HW: 7})
	res := <-sink.ch
	require.NoError(t, res.Err)
	require.Equal(t, uint64(7), res.QuorumCommit.Receipt.HW)
	require.Equal(t, ch.OpID(1), res.Fence.OpID)
	// A duplicate callback cannot publish again or release budget twice.
	log.complete(0, replication.Receipt{HW: 9})
	require.NoError(t, pool.Submit(context.Background(), quorumCommitTask(3)))
	<-log.submitted
	require.ErrorIs(t, pool.Submit(context.Background(), quorumCommitTask(4)), ch.ErrBackpressured)
	require.Equal(t, 1, sink.Len())
	require.Zero(t, log.syncCalls)
}

func TestPoolQuorumSubmitTaskContextStaysAliveUntilCompletion(t *testing.T) {
	log := &asyncQuorumLog{submitted: make(chan replication.Proposal, 1)}
	sink := &captureSink{ch: make(chan Result, 1)}
	pool, err := NewPool(PoolConfig{Name: "q", Workers: 1, QueueSize: 1}, Deps{QuorumLog: log}, sink)
	require.NoError(t, err)
	defer pool.Close()
	caller, cancel := context.WithCancel(context.Background())
	task := quorumCommitTask(1)
	task.Context = caller
	require.NoError(t, pool.Submit(context.Background(), task))
	<-log.submitted
	time.Sleep(10 * time.Millisecond)
	log.mu.Lock()
	require.NoError(t, log.ctxs[0].Err(), "worker return must not cancel the round")
	log.mu.Unlock()
	cancel()
	res := <-sink.ch
	require.ErrorIs(t, res.Err, context.Canceled)
}

func TestPoolCloseCancelsAndJoinsDeferredQuorumCommits(t *testing.T) {
	log := &asyncQuorumLog{submitted: make(chan replication.Proposal, 2)}
	sink := &captureSink{ch: make(chan Result, 2)}
	pool, err := NewPool(PoolConfig{Name: "q", Workers: 1, QueueSize: 1}, Deps{QuorumLog: log}, sink)
	require.NoError(t, err)
	require.NoError(t, pool.Submit(context.Background(), quorumCommitTask(1)))
	<-log.submitted
	require.NoError(t, pool.Close())
	// Close returns only after the deferred result was published.
	require.Equal(t, 1, sink.Len())
	require.Error(t, (<-sink.ch).Err)
}
