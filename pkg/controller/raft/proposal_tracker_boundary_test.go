package raft

import (
	"errors"
	"testing"
)

func TestProposalTrackerFailFromKeepsEarlierCommittedWorkEligible(t *testing.T) {
	tracker := newProposalTracker()
	responses := map[uint64]chan proposalResponse{}
	for _, index := range []uint64{2, 4, 6} {
		responses[index] = make(chan proposalResponse, 1)
		tracker.byIndex[index] = trackedProposal{resp: responses[index]}
	}
	leaderErr := errors.New("leadership changed")
	tracker.failFrom(4, leaderErr)
	if _, ok := tracker.byIndex[2]; !ok {
		t.Fatal("proposal before failure boundary was removed")
	}
	for _, index := range []uint64{4, 6} {
		if _, ok := tracker.byIndex[index]; ok {
			t.Fatalf("proposal %d remained after failFrom", index)
		}
		if response := <-responses[index]; !errors.Is(response.err, leaderErr) {
			t.Fatalf("proposal %d response error = %v", index, response.err)
		}
	}
	tracker.complete(2, ProposalResult{Changed: true, Revision: 3}, nil)
	if response := <-responses[2]; response.err != nil || !response.result.Changed || response.result.Revision != 3 {
		t.Fatalf("earlier proposal response = %+v", response)
	}
}
