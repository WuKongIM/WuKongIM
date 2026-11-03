//go:build integration

package raft

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A read barrier must prove current quorum and applied state without turning
// each cold Presence read into another durable Controller log entry.
func TestReadBarrierHealthyThreeVotersDoesNotAppend(t *testing.T) {
	cluster := newRaftTestCluster(t, []uint64{1, 2, 3})
	cluster.start(t)
	cluster.propose(t, testInitCommand("wk-read-barrier-no-append", cluster.peers))
	cluster.waitForRevision(t, 1)
	leader := cluster.waitForLeader(t)
	before := leader.service.Status().CommitIndex
	require.NotZero(t, before)
	for i := 0; i < 3; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		index, err := leader.service.ReadBarrier(ctx)
		cancel()
		require.NoError(t, err)
		require.Equal(t, before, index, "read barrier appended a new entry")
		require.Equal(t, before, leader.service.Status().CommitIndex, "read changed committed log boundary")
	}
	for _, node := range cluster.nodes {
		if node.id == leader.id {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, err := node.service.ReadBarrier(ctx)
		cancel()
		require.ErrorIs(t, err, ErrNotLeader, "the read API must not forward follower admission")
	}
}

func TestReadBarrierLifecycleAndCanceledAdmission(t *testing.T) {
	cluster := newRaftTestCluster(t, []uint64{1})
	node := cluster.nodes[0]
	_, err := node.service.ReadBarrier(context.Background())
	require.ErrorIs(t, err, ErrNotStarted)
	cluster.start(t)
	cluster.propose(t, testInitCommand("wk-read-barrier-lifecycle", cluster.peers))
	before := node.service.Status().CommitIndex
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = node.service.ReadBarrier(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, before, node.service.Status().CommitIndex)
	require.NoError(t, node.service.Stop())
	_, err = node.service.ReadBarrier(context.Background())
	require.ErrorIs(t, err, ErrStopped)
}
