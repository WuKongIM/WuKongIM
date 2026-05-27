package raft

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.etcd.io/raft/v3/raftpb"
)

func TestProposalTrackerBindsEntriesAndCompletesByIndex(t *testing.T) {
	tracker := newProposalTracker()
	resp1 := make(chan error, 1)
	resp2 := make(chan error, 1)
	tracker.enqueue(trackedProposal{resp: resp1})
	tracker.enqueue(trackedProposal{resp: resp2})

	tracker.bindAppended([]raftpb.Entry{
		{Index: 5, Type: raftpb.EntryNormal, Data: []byte("a")},
		{Index: 6, Type: raftpb.EntryNormal, Data: []byte("b")},
	})
	tracker.complete(5, nil)
	tracker.complete(6, ProposalRejectedError{Index: 6, Reason: "bad"})

	require.NoError(t, <-resp1)
	require.ErrorIs(t, <-resp2, ErrProposalRejected)
}

func TestProposalTrackerFailsUncommittedOnLeaderLoss(t *testing.T) {
	tracker := newProposalTracker()
	resp := make(chan error, 1)
	tracker.enqueue(trackedProposal{resp: resp})
	tracker.failUnbound(ErrNotLeader)
	require.ErrorIs(t, <-resp, ErrNotLeader)
}

func TestProposalTrackerBindsProbeToEmptyEntry(t *testing.T) {
	tracker := newProposalTracker()
	resp := make(chan error, 1)
	tracker.enqueue(trackedProposal{resp: resp, probe: true})

	tracker.bindAppended([]raftpb.Entry{{Index: 7, Type: raftpb.EntryNormal}})
	tracker.complete(7, nil)

	require.NoError(t, <-resp)
}

func TestProposalTrackerSkipsLeaderNoopForCommandProposal(t *testing.T) {
	tracker := newProposalTracker()
	resp := make(chan error, 1)
	tracker.enqueue(trackedProposal{resp: resp})

	tracker.bindAppended([]raftpb.Entry{{Index: 7, Type: raftpb.EntryNormal}})
	tracker.complete(7, nil)
	select {
	case err := <-resp:
		t.Fatalf("command proposal completed on empty leader no-op: %v", err)
	default:
	}

	tracker.bindAppended([]raftpb.Entry{{Index: 8, Type: raftpb.EntryNormal, Data: []byte("cmd")}})
	tracker.complete(8, nil)
	require.NoError(t, <-resp)
}

func TestProposalTrackerBindsCommandAndProbeInOrder(t *testing.T) {
	tracker := newProposalTracker()
	cmdResp := make(chan error, 1)
	probeResp := make(chan error, 1)
	tracker.enqueue(trackedProposal{resp: cmdResp})
	tracker.enqueue(trackedProposal{resp: probeResp, probe: true})

	tracker.bindAppended([]raftpb.Entry{
		{Index: 10, Type: raftpb.EntryNormal, Data: []byte("cmd")},
		{Index: 11, Type: raftpb.EntryNormal},
	})
	tracker.complete(10, nil)
	tracker.complete(11, nil)

	require.NoError(t, <-cmdResp)
	require.NoError(t, <-probeResp)
}
