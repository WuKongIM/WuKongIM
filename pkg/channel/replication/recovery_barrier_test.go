package replication

import (
	"context"
	"reflect"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

func TestCurrentTermBarrierIsDeterministicBusinessNeutralAndQuorumDurable(t *testing.T) {
	key := ch.ChannelKey("1:barrier")
	id := ch.ChannelID{ID: "barrier", Type: 1}
	mutation, tail := recoveryMutationAfter(t, key, id, 41, 0, ch.EntryIdentity{})
	recovered := ReplicaState{LEO: 1, Committed: 1, Manifest: mutation.Manifest, TailIdentity: tail}
	authority := Authority{
		Key: key, ChannelID: id,
		ID:     AuthorityID{ChannelEpoch: 3, LeaderTerm: 6, FenceVersion: 8},
		Leader: 1, Voters: []ch.NodeID{1, 2, 3}, WriteQuorum: 2,
	}
	firstDispatcher := &recordingBarrierDispatcher{}
	first, err := writeCurrentTermBarrier(context.Background(), authority, recovered, firstDispatcher)
	if err != nil {
		t.Fatalf("writeCurrentTermBarrier() error = %v", err)
	}
	if first.State.LEO != 2 || first.State.Committed != 1 || first.State.Manifest.LeaderTerm != authority.ID.LeaderTerm ||
		first.State.Manifest.ChannelEpoch != authority.ID.ChannelEpoch || first.State.Manifest.FenceVersion != authority.ID.FenceVersion ||
		first.State.TailIdentity.Index != 2 {
		t.Fatalf("barrier state = %+v, want current-term tail at 2 over committed prefix 1", first.State)
	}
	if len(firstDispatcher.proposals) != 3 {
		t.Fatalf("barrier submissions = %d, want local plus two followers", len(firstDispatcher.proposals))
	}
	proposal := firstDispatcher.proposals[0]
	if len(proposal.records) != 1 || !proposal.records[0].SyncOnce || proposal.records[0].FromUID != "" ||
		proposal.records[0].ClientMsgNo != "" || proposal.committed != recovered.LEO {
		t.Fatalf("barrier proposal = %+v, want business-neutral SyncOnce with prior committed frontier", proposal)
	}
	for _, submitted := range firstDispatcher.proposals[1:] {
		if !reflect.DeepEqual(submitted, proposal) {
			t.Fatalf("barrier submissions differ: first=%+v next=%+v", proposal, submitted)
		}
	}

	secondDispatcher := &recordingBarrierDispatcher{}
	second, err := writeCurrentTermBarrier(context.Background(), authority, recovered, secondDispatcher)
	if err != nil {
		t.Fatalf("writeCurrentTermBarrier(retry) error = %v", err)
	}
	if !reflect.DeepEqual(first, second) || len(secondDispatcher.proposals) != 3 ||
		!reflect.DeepEqual(secondDispatcher.proposals[0], proposal) {
		t.Fatalf("barrier retry = %+v/%+v, want exact deterministic replay %+v", second, secondDispatcher.proposals, first)
	}
}

type recordingBarrierDispatcher struct {
	proposals []durableProposal
}

func (d *recordingBarrierDispatcher) submitLocal(_ context.Context, proposal durableProposal, complete func(durabilityCompletion)) error {
	d.proposals = append(d.proposals, proposal)
	complete(durabilityCompletion{outcome: ch.AppendOutcomeDurable})
	return nil
}

func (d *recordingBarrierDispatcher) submitReplica(_ context.Context, node ch.NodeID, proposal durableProposal, complete func(durabilityCompletion)) error {
	d.proposals = append(d.proposals, proposal)
	complete(durabilityCompletion{outcome: ch.AppendOutcomeDurable, follower: 0})
	return nil
}
