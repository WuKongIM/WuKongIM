package raft

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.etcd.io/raft/v3/raftpb"
)

func TestFreshRaftStateIsTheOnlyBootstrapCandidate(t *testing.T) {
	tests := []struct {
		name    string
		startup runStartupState
		want    bool
	}{
		{name: "empty", want: true},
		{name: "wal entry", startup: runStartupState{LastIndex: 1}},
		{name: "applied metadata", startup: runStartupState{AppliedIndex: 1}},
		{name: "hard state", startup: runStartupState{HardState: raftpb.HardState{Term: 1}}},
		{name: "configuration", startup: runStartupState{ConfState: raftpb.ConfState{Voters: []uint64{1}}}},
		{name: "snapshot", startup: runStartupState{Snapshot: raftpb.Snapshot{Metadata: raftpb.SnapshotMetadata{Index: 1, Term: 1}}}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, shouldBootstrap(test.startup))
		})
	}
}

func TestOnlySmallestConfiguredPeerMayBootstrap(t *testing.T) {
	peers := []Peer{{NodeID: 9}, {NodeID: 2}, {NodeID: 5}}

	require.True(t, isSmallestPeer(2, peers))
	require.False(t, isSmallestPeer(5, peers))
	require.False(t, isSmallestPeer(1, peers))
	require.False(t, isSmallestPeer(1, nil))
}
