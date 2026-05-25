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
