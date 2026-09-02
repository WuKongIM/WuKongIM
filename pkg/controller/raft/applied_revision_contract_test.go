package raft

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/controller/fsm"
	"github.com/stretchr/testify/require"
	"go.etcd.io/raft/v3/raftpb"
)

func TestApplySchedulerSeparatesLogicalRevisionFromAppliedRaftProgress(t *testing.T) {
	ctx := context.Background()
	sm := newTestStateMachine(t, filepath.Join(t.TempDir(), "cluster-state.json"))
	marker := &fakeAppliedStore{}
	completions := make(map[uint64]proposalResponse)
	scheduler := newApplyScheduler(applySchedulerConfig{MaxEntries: 8, MaxBytes: 1 << 20}, sm, marker, func(index uint64, result ProposalResult, err error) {
		completions[index] = proposalResponse{result: result, err: err}
	})

	peers := []Peer{{NodeID: 1, Addr: "n1"}}
	changed := recoveryUpsertNodeCommand(1, 1, "node-1-renamed")
	idempotent := recoveryUpsertNodeCommand(1, 1, "node-1-renamed")
	rejected := recoveryUpsertNodeCommand(1, 1, "node-1-rejected")
	entries := []raftpb.Entry{
		recoveryCommandEntry(t, 2, testInitCommand("wk-applied-revision", peers)),
		recoveryCommandEntry(t, 3, changed),
		recoveryCommandEntry(t, 4, idempotent),
		recoveryCommandEntry(t, 5, rejected),
	}

	require.NoError(t, scheduler.applyEntries(ctx, entries, nil))
	require.Equal(t, []uint64{5}, marker.marks)
	require.True(t, completions[2].result.Changed)
	require.Equal(t, uint64(1), completions[2].result.Revision)
	require.True(t, completions[3].result.Changed)
	require.Equal(t, uint64(2), completions[3].result.Revision)
	require.True(t, completions[4].result.Noop)
	require.Equal(t, fsm.ReasonNoChange, completions[4].result.Reason)
	require.Equal(t, uint64(2), completions[4].result.Revision)
	require.Equal(t, uint64(4), completions[4].result.AppliedRaftIndex)
	require.ErrorIs(t, completions[5].err, ErrProposalRejected)
	require.True(t, completions[5].result.Rejected)
	require.Equal(t, fsm.ReasonExpectedRevisionMismatch, completions[5].result.Reason)
	require.Equal(t, uint64(2), completions[5].result.Revision)
	require.Equal(t, uint64(5), completions[5].result.AppliedRaftIndex)

	materialized := sm.Snapshot(ctx)
	require.Equal(t, uint64(2), materialized.Revision)
	require.Equal(t, uint64(5), materialized.AppliedRaftIndex)
	require.Equal(t, "node-1-renamed", recoveryNodeName(t, materialized, 1))

	require.NoError(t, scheduler.applyEntries(ctx, []raftpb.Entry{{Index: 6, Term: 1, Type: raftpb.EntryNormal}}, nil))
	require.Equal(t, []uint64{5, 6}, marker.marks)
	require.True(t, completions[6].result.Noop)
	require.Equal(t, uint64(6), completions[6].result.AppliedRaftIndex)
	require.Equal(t, materialized, sm.Snapshot(ctx))
}
