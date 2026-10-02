package multiraft

import (
	"crypto/sha256"
	"testing"

	"github.com/stretchr/testify/require"
	"go.etcd.io/raft/v3/raftpb"
)

func TestCheckpointProofRequiresExactHistoryAndIdentity(t *testing.T) {
	metadata := raftpb.SnapshotMetadata{Index: 1, Term: 1, ConfState: raftpb.ConfState{Voters: []uint64{1}}}
	base := newCheckpointProof("cluster-a", 1, 7, sha256.Sum256([]byte("owned-slots")), metadata, sha256.Sum256([]byte("snapshot")), 1)
	entries := []raftpb.Entry{{Index: 2, Term: 1, Type: raftpb.EntryNormal, Data: []byte("first")}, {Index: 3, Term: 2, Type: raftpb.EntryNormal, Data: []byte("second")}}
	proof := base
	var err error
	for _, entry := range entries {
		proof, err = advanceCheckpointProof(proof, entry, metadata.ConfState, 1)
		require.NoError(t, err)
	}
	require.True(t, verifyCheckpointProof(base, proof, entries, 3))
	for _, tc := range []struct {
		name   string
		change func(*RecoveryCheckpoint)
	}{
		{"unsupported", func(p *RecoveryCheckpoint) { p.Version++ }},
		{"cluster", func(p *RecoveryCheckpoint) { p.ClusterID = "other" }},
		{"node", func(p *RecoveryCheckpoint) { p.NodeID++ }},
		{"slot", func(p *RecoveryCheckpoint) { p.SlotID++ }},
		{"ownership", func(p *RecoveryCheckpoint) { p.OwnershipDigest[0]++ }},
		{"snapshot", func(p *RecoveryCheckpoint) { p.SnapshotDigest[0]++ }},
		{"term", func(p *RecoveryCheckpoint) { p.AppliedTerm++ }},
		{"membership", func(p *RecoveryCheckpoint) { p.ConfState = raftpb.ConfState{Voters: []uint64{1, 2}} }},
		{"membership-index", func(p *RecoveryCheckpoint) { p.ConfigAppliedIndex++ }},
		{"future-index", func(p *RecoveryCheckpoint) { p.AppliedIndex++ }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := proof
			tc.change(&changed)
			require.False(t, verifyCheckpointProof(base, changed, entries, 3))
		})
	}
	changed := append([]raftpb.Entry(nil), entries...)
	changed[0].Data = []byte("different at identical index and term")
	require.False(t, verifyCheckpointProof(base, proof, changed, 3))
	require.False(t, verifyCheckpointProof(base, proof, entries[1:], 3), "missing prefix")
	require.False(t, verifyCheckpointProof(base, proof, entries[:1], 3), "missing suffix")
	require.False(t, verifyCheckpointProof(base, proof, entries, 2), "checkpoint exceeds durable commit")
}

func TestCheckpointProofReconstructsMembershipAtAppliedBoundary(t *testing.T) {
	metadata := raftpb.SnapshotMetadata{Index: 1, Term: 1, ConfState: raftpb.ConfState{Voters: []uint64{1}}}
	base := newCheckpointProof("cluster", 1, 1, sha256.Sum256(nil), metadata, sha256.Sum256([]byte("snapshot")), 1)
	change := raftpb.ConfChange{Type: raftpb.ConfChangeAddLearnerNode, NodeID: 2}
	data, err := change.Marshal()
	require.NoError(t, err)
	entries := []raftpb.Entry{{Index: 2, Term: 1, Type: raftpb.EntryConfChange, Data: data}, {Index: 3, Term: 1, Type: raftpb.EntryNormal, Data: []byte("mutation")}}
	conf := raftpb.ConfState{Voters: []uint64{1}, Learners: []uint64{2}}
	proof := base
	for _, entry := range entries {
		proof, err = advanceCheckpointProof(proof, entry, conf, 2)
		require.NoError(t, err)
	}
	require.True(t, verifyCheckpointProof(base, proof, entries, 3))
	// A committed suffix after the checkpoint remains for replay; it must exist.
	entries = append(entries, raftpb.Entry{Index: 4, Term: 2, Type: raftpb.EntryNormal})
	require.True(t, verifyCheckpointProof(base, proof, entries, 4))
	require.False(t, verifyCheckpointProof(base, proof, entries[:2], 4))
	encoded, err := encodeCheckpointProof(proof)
	require.NoError(t, err)
	decoded, ok := decodeCheckpointProof(encoded)
	require.True(t, ok)
	require.True(t, verifyCheckpointProof(base, decoded, entries, 4))
	_, ok = decodeCheckpointProof(append(encoded, []byte(" trailing")...))
	require.False(t, ok)
}
