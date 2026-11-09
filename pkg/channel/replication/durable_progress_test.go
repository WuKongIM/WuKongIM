package replication

import (
	"context"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

// Failure cases this tracker must prevent (retention safety, see
// docs/superpowers/specs/2026-11-09-quorum-mode-retention-safety-design.md):
//   - a proof from an older authority reported under the current one;
//   - a proof moving a voter backwards;
//   - an unknown voter reported as covered.
func TestDurableProgressFencesAuthorityAndNeverRegresses(t *testing.T) {
	var p durableProgress
	a := AuthorityID{ChannelEpoch: 1, LeaderTerm: 1}
	b := AuthorityID{ChannelEpoch: 1, LeaderTerm: 2}
	p.reset(a)
	p.record(a, 2, 5)
	p.record(a, 2, 3)
	p.record(a, 3, 7)
	p.record(b, 3, 9)
	p.record(a, 0, 9)
	id, got := p.snapshot()
	if id != a || len(got) != 2 || got[2] != 5 || got[3] != 7 {
		t.Fatalf("snapshot = %v %v, want authority a with 2->5 3->7", id, got)
	}
	got[2] = 100
	if _, again := p.snapshot(); again[2] != 5 {
		t.Fatal("snapshot exposed mutable tracker state")
	}
	p.reset(b)
	if id, got := p.snapshot(); id != b || len(got) != 0 {
		t.Fatalf("authority advance kept old proofs: %v %v", id, got)
	}
	p.record(a, 2, 50)
	if _, got := p.snapshot(); len(got) != 0 {
		t.Fatal("late proof from the previous authority was accepted")
	}
}

// A round must report every voter's exact durable proof, including the
// trailing follower written after quorum, and nothing else.
func TestDurableRoundReportsEveryVoterProofIncludingDeferred(t *testing.T) {
	dispatcher := &proofDispatcher{follower: map[ch.NodeID]durabilityCompletion{
		2: {outcome: ch.AppendOutcomeDurable},
		3: {outcome: ch.AppendOutcomeAlreadyDurable},
	}}
	proofs := make(chan [2]uint64, 4)
	sink := func(voter ch.NodeID, through uint64) { proofs <- [2]uint64{uint64(voter), through} }
	_, err := runDurableRoundWithProofs(context.Background(), 1, []ch.NodeID{1, 2, 3}, 2, durableProposal{first: 4, last: 6, channelKey: "1:proofs"}, dispatcher, sink)
	if err != nil {
		t.Fatalf("runDurableRound() error = %v", err)
	}
	seen := map[uint64]uint64{}
	deadline := time.After(time.Second)
	for len(seen) < 3 {
		select {
		case p := <-proofs:
			seen[p[0]] = p[1]
		case <-deadline:
			t.Fatalf("proofs = %v, want all three voters", seen)
		}
	}
	for voter, through := range seen {
		if through != 6 {
			t.Fatalf("voter %d proof through %d, want proposal last 6", voter, through)
		}
	}
}

func TestDurableRoundNeverReportsUnprovenOutcomes(t *testing.T) {
	for name, completion := range map[string]durabilityCompletion{
		"unknown":      {outcome: ch.AppendOutcomeUnknown, err: errPeerOutcomeUnknown},
		"conflict":     {outcome: ch.AppendOutcomeConflict, err: ch.ErrLogConflict},
		"needs repair": {outcome: ch.AppendOutcomeDefinitelyNotWritten, err: errReplicaNeedsRepair, follower: 2, needFrom: 3},
		"durable+err":  {outcome: ch.AppendOutcomeDurable, err: errPeerOutcomeUnknown},
	} {
		t.Run(name, func(t *testing.T) {
			dispatcher := &proofDispatcher{follower: map[ch.NodeID]durabilityCompletion{2: completion, 3: completion}}
			var reported []ch.NodeID
			sink := func(voter ch.NodeID, _ uint64) { reported = append(reported, voter) }
			_, _ = runDurableRoundWithProofs(context.Background(), 1, []ch.NodeID{1, 2, 3}, 2, durableProposal{first: 1, last: 1, channelKey: "1:unproven"}, dispatcher, sink)
			for _, voter := range reported {
				if voter != 1 {
					t.Fatalf("unproven follower %d reported as durable", voter)
				}
			}
		})
	}
}

// proofDispatcher completes every write inline, including deferred writes.
type proofDispatcher struct {
	follower map[ch.NodeID]durabilityCompletion
}

func (d *proofDispatcher) submitLocal(_ context.Context, _ durableProposal, complete func(durabilityCompletion)) error {
	complete(durabilityCompletion{outcome: ch.AppendOutcomeDurable})
	return nil
}

func (d *proofDispatcher) submitReplica(_ context.Context, node ch.NodeID, _ durableProposal, complete func(durabilityCompletion)) error {
	complete(d.follower[node])
	return nil
}

func (d *proofDispatcher) submitReplicaDeferred(_ context.Context, node ch.NodeID, _ durableProposal, complete func(durabilityCompletion)) error {
	complete(d.follower[node])
	return nil
}

// The real quorum log must expose every voter's proof after a commit, discard
// proofs on authority advance, and reseed them from the new barrier.
func TestQuorumLogDurableProgressFollowsCommitsAndAuthority(t *testing.T) {
	harness := newReplicaHarness(t, 1, 2, 3)
	log, err := newQuorumLog(quorumLogConfig{
		Local: 1, Store: harness.stores[1], Recovery: harness, Durability: harness,
		RecoveryTimeout: time.Minute, RecoveryPageBytes: 64 << 10,
		MaxChannels: 8, MaxVoters: 3, MaxProposalRecords: 256, MaxProposalBytes: 64 << 10, MaxRetainedCommands: 16,
	})
	if err != nil {
		t.Fatalf("newQuorumLog() error = %v", err)
	}
	var _ DurableProgressReader = log
	first := Authority{
		Key: "1:durable-progress", ChannelID: ch.ChannelID{ID: "durable-progress", Type: 1},
		ID:     AuthorityID{ChannelEpoch: 3, LeaderTerm: 5, FenceVersion: 7},
		Leader: 1, Voters: []ch.NodeID{1, 2, 3}, WriteQuorum: 2,
	}
	if _, _, ok := log.DurableProgress(first.Key); ok {
		t.Fatal("unloaded Channel reported progress")
	}
	if _, err := log.Install(context.Background(), first); err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if _, err := log.Commit(context.Background(), Proposal{
		Key: first.Key, Expected: first.ID, CommandID: ch.CommandID{31: 1},
		Records: []ch.Record{{ID: 1, Epoch: 3, FromUID: "u", ClientMsgNo: "c1", Payload: []byte("p"), SizeBytes: 1, ServerTimestampMS: 1}},
	}); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	requireDurableProgress(t, log, first.Key, first.ID, map[ch.NodeID]uint64{1: 1, 2: 1, 3: 1})

	second := first
	second.ID = AuthorityID{ChannelEpoch: 3, LeaderTerm: 6, FenceVersion: 7}
	if _, err := log.Install(context.Background(), second); err != nil {
		t.Fatalf("Install(second) error = %v", err)
	}
	// The non-empty foreign frontier writes the current-term barrier at 2.
	requireDurableProgress(t, log, second.Key, second.ID, map[ch.NodeID]uint64{1: 2, 2: 2, 3: 2})

	if !log.Release(second.Key, second.ID) {
		t.Fatal("Release() = false")
	}
	if _, _, ok := log.DurableProgress(second.Key); ok {
		t.Fatal("released Channel still reported progress")
	}
}

func requireDurableProgress(t *testing.T, log *quorumLog, key ch.ChannelKey, authority AuthorityID, want map[ch.NodeID]uint64) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		id, got, ok := log.DurableProgress(key)
		match := ok && id == authority && len(got) == len(want)
		for voter, through := range want {
			match = match && got[voter] == through
		}
		if match {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("DurableProgress = %v %v %v, want %v %v", id, got, ok, authority, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
