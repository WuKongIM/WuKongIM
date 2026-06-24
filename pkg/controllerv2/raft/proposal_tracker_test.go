package raft

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.etcd.io/raft/v3/raftpb"
)

func TestProposalTrackerBindsEntriesAndCompletesByIndex(t *testing.T) {
	tracker := newProposalTracker()
	resp1 := make(chan proposalResponse, 1)
	resp2 := make(chan proposalResponse, 1)
	tracker.enqueue(trackedProposal{resp: resp1})
	tracker.enqueue(trackedProposal{resp: resp2})

	tracker.bindAppended([]raftpb.Entry{
		{Index: 5, Type: raftpb.EntryNormal, Data: []byte("a")},
		{Index: 6, Type: raftpb.EntryNormal, Data: []byte("b")},
	})
	tracker.complete(5, ProposalResult{Changed: true, Revision: 2, AppliedRaftIndex: 5}, nil)
	tracker.complete(6, ProposalResult{Rejected: true, Reason: "bad", Revision: 2, AppliedRaftIndex: 6}, ProposalRejectedError{Index: 6, Reason: "bad"})

	got1 := <-resp1
	require.NoError(t, got1.err)
	require.True(t, got1.result.Changed)
	require.Equal(t, uint64(5), got1.result.AppliedRaftIndex)
	got2 := <-resp2
	require.ErrorIs(t, got2.err, ErrProposalRejected)
	require.True(t, got2.result.Rejected)
	require.Equal(t, "bad", got2.result.Reason)
}

func TestProposalTrackerFailsUncommittedOnLeaderLoss(t *testing.T) {
	tracker := newProposalTracker()
	resp := make(chan proposalResponse, 1)
	tracker.enqueue(trackedProposal{resp: resp})
	tracker.failUnbound(ErrNotLeader)
	require.ErrorIs(t, (<-resp).err, ErrNotLeader)
}

func TestProposalTrackerBindsProbeToEmptyEntry(t *testing.T) {
	tracker := newProposalTracker()
	resp := make(chan proposalResponse, 1)
	tracker.enqueue(trackedProposal{resp: resp, probe: true})

	tracker.bindAppended([]raftpb.Entry{{Index: 7, Type: raftpb.EntryNormal}})
	tracker.complete(7, ProposalResult{Noop: true, AppliedRaftIndex: 7}, nil)

	got := <-resp
	require.NoError(t, got.err)
	require.True(t, got.result.Noop)
	require.Equal(t, uint64(7), got.result.AppliedRaftIndex)
}

func TestProposalTrackerSkipsLeaderNoopForCommandProposal(t *testing.T) {
	tracker := newProposalTracker()
	resp := make(chan proposalResponse, 1)
	tracker.enqueue(trackedProposal{resp: resp})

	tracker.bindAppended([]raftpb.Entry{{Index: 7, Type: raftpb.EntryNormal}})
	tracker.complete(7, ProposalResult{Noop: true, AppliedRaftIndex: 7}, nil)
	select {
	case got := <-resp:
		t.Fatalf("command proposal completed on empty leader no-op: %#v", got)
	default:
	}

	tracker.bindAppended([]raftpb.Entry{{Index: 8, Type: raftpb.EntryNormal, Data: []byte("cmd")}})
	tracker.complete(8, ProposalResult{Changed: true, AppliedRaftIndex: 8}, nil)
	require.NoError(t, (<-resp).err)
}

func TestProposalTrackerBindsCommandAndProbeInOrder(t *testing.T) {
	tracker := newProposalTracker()
	cmdResp := make(chan proposalResponse, 1)
	probeResp := make(chan proposalResponse, 1)
	tracker.enqueue(trackedProposal{resp: cmdResp})
	tracker.enqueue(trackedProposal{resp: probeResp, probe: true})

	tracker.bindAppended([]raftpb.Entry{
		{Index: 10, Type: raftpb.EntryNormal, Data: []byte("cmd")},
		{Index: 11, Type: raftpb.EntryNormal},
	})
	tracker.complete(10, ProposalResult{Changed: true, AppliedRaftIndex: 10}, nil)
	tracker.complete(11, ProposalResult{Noop: true, AppliedRaftIndex: 11}, nil)

	require.NoError(t, (<-cmdResp).err)
	require.NoError(t, (<-probeResp).err)
}
