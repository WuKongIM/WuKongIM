package reactor

import (
	"context"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/machine"
	"github.com/WuKongIM/WuKongIM/pkg/channel/replication"
	"github.com/stretchr/testify/require"
)

// Under durable quorum the reactor never sees follower ACKs, so ISR safety
// must come from the quorum log's per-voter proofs. Failure cases:
//   - an unproven ISR member reported as covered (data could be trimmed early);
//   - proofs from another authority trusted;
//   - legacy ACK progress ignored when it is ahead.
func TestMinISRMatchUsesQuorumDurableProofs(t *testing.T) {
	state := &machine.ChannelState{LocalNode: 1, LEO: 10, HW: 10, CheckpointHW: 10, Role: ch.RoleLeader,
		ISR: []ch.NodeID{1, 2, 3}, Progress: map[ch.NodeID]machine.ReplicaProgress{1: {Match: 10}}}
	require.Zero(t, minISRMatchOffset(state, nil), "no proofs: followers are unknown")
	require.Zero(t, minISRMatchOffset(state, map[ch.NodeID]uint64{2: 8}), "voter 3 is still unproven")
	require.Equal(t, uint64(5), minISRMatchOffset(state, map[ch.NodeID]uint64{2: 8, 3: 5}))
	state.Progress[3] = machine.ReplicaProgress{Match: 7}
	require.Equal(t, uint64(7), minISRMatchOffset(state, map[ch.NodeID]uint64{2: 8, 3: 5}), "the higher of ACK and proof wins")

	allowed, reason := retentionTrimDecision(state, 6, map[ch.NodeID]uint64{2: 8, 3: 5})
	require.True(t, allowed, reason)
	allowed, reason = retentionTrimDecision(state, 8, map[ch.NodeID]uint64{2: 8})
	require.False(t, allowed)
	require.Equal(t, ch.RetentionBlockedMinISRLag, reason)
}

func TestQuorumDurableMatchesRequiresInstalledAuthority(t *testing.T) {
	installed := replication.AuthorityID{ChannelEpoch: 1, LeaderTerm: 4, FenceVersion: 2}
	reader := &fakeDurableProgress{authority: installed, through: map[ch.NodeID]uint64{2: 9, 3: 9}, ok: true}
	r := &Reactor{cfg: ReactorConfig{LocalNode: 1, QuorumLog: reader}}
	rc := &runtimeChannel{state: &machine.ChannelState{Key: "1:q"}}

	require.Nil(t, r.quorumDurableMatches(rc), "an uninstalled runtime must not borrow proofs")
	rc.quorumAuthority = replication.Authority{ID: installed}
	require.Equal(t, map[ch.NodeID]uint64{2: 9, 3: 9}, r.quorumDurableMatches(rc))
	rc.quorumAuthority = replication.Authority{ID: replication.AuthorityID{ChannelEpoch: 1, LeaderTerm: 5, FenceVersion: 2}}
	require.Nil(t, r.quorumDurableMatches(rc), "proofs from another authority are not evidence")

	r.cfg.QuorumLog = nil
	require.Nil(t, r.quorumDurableMatches(rc), "legacy replication has no proof source")
}

type fakeDurableProgress struct {
	authority replication.AuthorityID
	through   map[ch.NodeID]uint64
	ok        bool
}

func (f *fakeDurableProgress) Install(context.Context, replication.Authority) (replication.Installed, error) {
	return replication.Installed{}, nil
}
func (f *fakeDurableProgress) Commit(context.Context, replication.Proposal) (replication.Receipt, error) {
	return replication.Receipt{}, nil
}
func (f *fakeDurableProgress) Release(ch.ChannelKey, replication.AuthorityID) bool { return true }
func (f *fakeDurableProgress) DurableProgress(ch.ChannelKey) (replication.AuthorityID, map[ch.NodeID]uint64, bool) {
	return f.authority, f.through, f.ok
}
