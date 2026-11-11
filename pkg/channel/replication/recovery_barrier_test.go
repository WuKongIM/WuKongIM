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
	first, err := writeCurrentTermBarrier(context.Background(), authority, recovered, firstDispatcher, nil)
	if err != nil {
		t.Fatalf("writeCurrentTermBarrier(, nil) error = %v", err)
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
	second, err := writeCurrentTermBarrier(context.Background(), authority, recovered, secondDispatcher, nil)
	if err != nil {
		t.Fatalf("writeCurrentTermBarrier(retry, nil) error = %v", err)
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

func TestAuthorityBarrierRejectsEqualOrRegressiveFence(t *testing.T) {
	key := ch.ChannelKey("1:fence-order")
	id := ch.ChannelID{ID: "fence-order", Type: 1}
	mutation, tail := recoveryMutationAfter(t, key, id, 42, 0, ch.EntryIdentity{})
	recovered := ReplicaState{LEO: 1, Committed: 1, Manifest: mutation.Manifest, TailIdentity: tail}
	for _, version := range []uint64{tail.FenceVersion, tail.FenceVersion - 1} {
		authority := Authority{Key: key, ChannelID: id, ID: AuthorityID{ChannelEpoch: tail.ChannelEpoch, LeaderTerm: tail.LeaderTerm, FenceVersion: version}, Leader: 1, Voters: []ch.NodeID{1, 2, 3}, WriteQuorum: 2}
		dispatcher := &recordingBarrierDispatcher{}
		if _, err := writeCurrentTermBarrier(context.Background(), authority, recovered, dispatcher, nil); err == nil || len(dispatcher.proposals) != 0 {
			t.Fatalf("version=%d err=%v writes=%d", version, err, len(dispatcher.proposals))
		}
	}
}

func TestAuthorityRejectsInvalidLearnersAndFreezesMembership(t *testing.T) {
	base := Authority{Key: "1:learners", ChannelID: ch.ChannelID{ID: "learners", Type: 1}, ID: AuthorityID{ChannelEpoch: 1, LeaderTerm: 1, FenceVersion: 1}, Leader: 1, Voters: []ch.NodeID{1, 2, 3}, WriteQuorum: 2}
	for _, learners := range [][]ch.NodeID{{0}, {2}, {4, 4}} {
		bad := base
		bad.Learners = learners
		if validAuthority(bad) {
			t.Fatalf("accepted invalid learners %v", learners)
		}
	}
	base.Learners = []ch.NodeID{4}
	if !validAuthority(base) {
		t.Fatal("valid learner rejected")
	}
	copied := cloneAuthority(base)
	base.Learners[0] = 5
	if copied.Learners[0] != 4 || sameAuthority(base, copied) {
		t.Fatal("learner membership was not frozen and fenced")
	}
	base.Leader = 5
	if validAuthority(base) {
		t.Fatal("learner was accepted as leader")
	}
}
