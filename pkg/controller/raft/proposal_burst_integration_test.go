//go:build integration

package raft

import (
	"context"
	"errors"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controller/command"
	"github.com/WuKongIM/WuKongIM/pkg/controller/fsm"
	"github.com/stretchr/testify/require"
	"go.etcd.io/raft/v3/raftpb"
)

// Mixed accepted, rejected and probe futures must retain their own receipts
// when a burst shares the same Ready and after the single-node cluster restarts.
func TestControllerProposalBurstPreservesReceiptsAndRestart(t *testing.T) {
	cluster := newRaftTestCluster(t, []uint64{1})
	cluster.start(t)
	init := testInitCommand("wk-proposal-burst", cluster.peers)
	init.Init.Config.HashSlotCount = 256
	init.Init.Config.SlotCount = 12
	init.Init.Config.ReplicaCount = 1
	cluster.propose(t, init)
	cluster.waitForRevision(t, 1)
	node := cluster.waitForLeader(t)
	before := node.service.Status().CommitIndex

	const count = 64
	type outcome struct {
		result ProposalResult
		err    error
	}
	out := make([]outcome, count)
	start := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var callers sync.WaitGroup
	for i := range out {
		callers.Add(1)
		go func(i int) {
			defer callers.Done()
			<-start
			switch i % 3 {
			case 0:
				out[i].result, out[i].err = node.service.ProposeResult(ctx, init)
			case 1:
				out[i].result, out[i].err = node.service.ProposeResult(ctx, testUpsertNodeCommand(999, 1, "must-not-apply"))
			case 2:
				out[i].err = node.service.ProbePropose(ctx)
			}
		}(i)
	}
	close(start)
	callers.Wait()
	indexes := make([]uint64, 0, count)
	seen := make(map[uint64]bool, count)
	for i, o := range out {
		switch i % 3 {
		case 0:
			require.NoError(t, o.err)
			require.True(t, o.result.Noop)
			require.False(t, o.result.Rejected)
		case 1:
			require.ErrorIs(t, o.err, ErrProposalRejected)
			require.True(t, o.result.Rejected)
			require.Equal(t, fsm.ReasonExpectedRevisionMismatch, o.result.Reason)
		case 2:
			require.NoError(t, o.err)
			continue
		}
		require.Greater(t, o.result.AppliedRaftIndex, before)
		require.False(t, seen[o.result.AppliedRaftIndex], "two futures received one entry's receipt")
		seen[o.result.AppliedRaftIndex] = true
		indexes = append(indexes, o.result.AppliedRaftIndex)
	}
	sort.Slice(indexes, func(i, j int) bool { return indexes[i] < indexes[j] })
	after := node.service.Status().CommitIndex
	require.Equal(t, before+count, after)
	st := node.stateMachine.Snapshot(context.Background())
	require.Equal(t, uint64(1), st.Revision)
	require.Equal(t, "n1", findTestNode(t, st, 1).Name)
	require.NoError(t, node.service.Stop())
	node.stateMachine = newTestStateMachine(t, node.statePath)
	cluster.rebuildService(t, node)
	cluster.start(t)
	barrier, err := node.service.ReadBarrier(ctx)
	require.NoError(t, err)
	require.GreaterOrEqual(t, barrier, after, "the restarted quorum/durable-applied cut lost an acknowledged entry")
	st = node.stateMachine.Snapshot(context.Background())
	require.Equal(t, uint64(1), st.Revision)
	// Empty probes advance the durable marker, not the materialized FSM file.
	require.GreaterOrEqual(t, st.AppliedRaftIndex, indexes[len(indexes)-1])
	require.Equal(t, "n1", findTestNode(t, st, 1).Name)
	t.Logf("joined=%d committed_before=%d committed_after=%d exact_receipt_indexes=%v restart_barrier=%d restart_fsm_applied=%d", count, before, after, indexes, barrier, st.AppliedRaftIndex)
}

// A peer's forwarded entry is not the next local future, even when both are
// pending together. Local callers must never receive that entry's rejection.
func TestControllerProposalBurstKeepsPeerEntriesSeparate(t *testing.T) {
	cluster := newRaftTestCluster(t, []uint64{1, 2, 3})
	cluster.start(t)
	init := testInitCommand("wk-proposal-peer-burst", cluster.peers)
	init.Init.Config.HashSlotCount = 256
	init.Init.Config.SlotCount = 12
	cluster.propose(t, init)
	cluster.waitForRevision(t, 1)
	leader := cluster.waitForLeader(t)
	var peer uint64
	for _, node := range cluster.nodes {
		if node.id != leader.id {
			peer = node.id
			break
		}
	}
	data, err := command.Encode(testUpsertNodeCommand(999, 1, "peer-must-not-apply"))
	require.NoError(t, err)
	before := leader.service.Status().CommitIndex
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	start := make(chan struct{})
	const locals, forwarded = 32, 16
	results := make([]ProposalResult, locals)
	errs := make([]error, locals+forwarded)
	var callers sync.WaitGroup
	for i := range errs {
		callers.Add(1)
		go func(i int) {
			defer callers.Done()
			<-start
			if i < locals {
				results[i], errs[i] = leader.service.ProposeResult(ctx, init)
			} else {
				errs[i] = leader.service.Step(ctx, raftpb.Message{Type: raftpb.MsgProp, From: peer, To: leader.id, Entries: []raftpb.Entry{{Type: raftpb.EntryNormal, Data: data}}})
			}
		}(i)
	}
	close(start)
	callers.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}
	seen := make(map[uint64]bool, locals)
	for _, result := range results {
		require.True(t, result.Noop)
		require.False(t, result.Rejected, "local receipt was replaced with a peer's result")
		require.False(t, seen[result.AppliedRaftIndex])
		seen[result.AppliedRaftIndex] = true
	}
	require.Eventually(t, func() bool { return leader.service.Status().CommitIndex >= before+locals+forwarded }, 5*time.Second, 10*time.Millisecond)
	_, err = leader.service.ReadBarrier(ctx)
	require.NoError(t, err)
	st := leader.stateMachine.Snapshot(context.Background())
	require.Equal(t, uint64(1), st.Revision)
	require.Equal(t, "n1", findTestNode(t, st, 1).Name)
	t.Logf("local_callers_joined=%d forwarded_admissions_joined=%d exact_local_receipts=%d applied=%d", locals, forwarded, len(seen), st.AppliedRaftIndex)
}

func TestControllerStopJoinsWithQueuedPeerResponses(t *testing.T) {
	cluster := newRaftTestCluster(t, []uint64{1, 2, 3})
	cluster.start(t)
	cluster.propose(t, testInitCommand("wk-step-pressure-stop", cluster.peers))
	cluster.waitForRevision(t, 1)
	leader := cluster.waitForLeader(t)
	var peer uint64
	for _, node := range cluster.nodes {
		if node.id != leader.id {
			peer = node.id
			break
		}
	}
	status := leader.service.Status()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	var callers sync.WaitGroup
	t.Cleanup(func() { cancel(); callers.Wait() })
	var admitted atomic.Uint64
	ready := make(chan struct{})
	var readyOnce sync.Once
	results := make(chan error, 8)
	for i := 0; i < cap(results); i++ {
		callers.Add(1)
		go func() {
			defer callers.Done()
			for {
				err := leader.service.Step(ctx, raftpb.Message{Type: raftpb.MsgAppResp, From: peer, To: leader.id, Term: status.Term, Index: status.CommitIndex})
				if err != nil {
					results <- err
					return
				}
				if admitted.Add(1) >= 2048 {
					readyOnce.Do(func() { close(ready) })
				}
			}
		}()
	}
	select {
	case <-ready:
	case <-ctx.Done():
		t.Fatal("peer-pressure fixture never admitted its bounded prefix")
	}
	stopped := make(chan error, 1)
	go func() { stopped <- leader.service.Stop() }()
	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("Stop did not join while peer inputs remained available")
	}
	cancel()
	callers.Wait()
	for i := 0; i < cap(results); i++ {
		err := <-results
		require.True(t, errors.Is(err, ErrStopped) || errors.Is(err, context.Canceled))
	}
	t.Logf("admitted_peer_responses=%d producers_joined=8 service_joined=true", admitted.Load())
}
