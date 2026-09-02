package raft

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.etcd.io/raft/v3/raftpb"
)

func TestProposalFuturesFailTogetherOnLeadershipLoss(t *testing.T) {
	tracker := newProposalTracker()
	bound := make(chan proposalResponse, 1)
	unbound := make(chan proposalResponse, 1)
	tracker.enqueue(trackedProposal{resp: bound})
	tracker.bindAppended([]raftpb.Entry{{Index: 7, Type: raftpb.EntryNormal, Data: []byte("command")}})
	tracker.enqueue(trackedProposal{resp: unbound, probe: true})

	tracker.failAll(ErrNotLeader)

	require.ErrorIs(t, (<-bound).err, ErrNotLeader)
	require.ErrorIs(t, (<-unbound).err, ErrNotLeader)
	require.Empty(t, tracker.queue)
	require.Empty(t, tracker.byIndex)

	tracker.complete(7, ProposalResult{Changed: true, AppliedRaftIndex: 7}, nil)
	select {
	case response := <-bound:
		t.Fatalf("bound future completed twice after leadership loss: %#v", response)
	default:
	}
}

func TestMembershipFutureReturnsCommittedConfiguration(t *testing.T) {
	tracker := newProposalTracker()
	response := make(chan proposalResponse, 1)
	tracker.enqueue(trackedProposal{resp: response, confChange: true})
	tracker.bindAppended([]raftpb.Entry{{Index: 8, Type: raftpb.EntryConfChange}})

	want := MembershipChangeResult{
		Index:     8,
		ConfState: raftpb.ConfState{Voters: []uint64{1, 2}, Learners: []uint64{3}},
	}
	tracker.completeMembership(8, want, nil)

	got := <-response
	require.NoError(t, got.err)
	require.Equal(t, want, got.membership)
	require.Empty(t, tracker.byIndex)
}
