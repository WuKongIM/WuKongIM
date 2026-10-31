package raft

import (
	"io"
	"log"
	"testing"

	"github.com/stretchr/testify/require"
	etcdraft "go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
)

func TestStepInputCostRetainsBytesWhenRaftRewritesConfiguration(t *testing.T) {
	storage := etcdraft.NewMemoryStorage()
	require.NoError(t, storage.ApplySnapshot(raftpb.Snapshot{Metadata: raftpb.SnapshotMetadata{Index: 1, Term: 1, ConfState: raftpb.ConfState{Voters: []uint64{1}}}}))
	require.NoError(t, storage.SetHardState(raftpb.HardState{Term: 1, Vote: 1, Commit: 1}))
	raw, err := etcdraft.NewRawNode(&etcdraft.Config{ID: 1, ElectionTick: 10, HeartbeatTick: 1, Applied: 1, Storage: storage, MaxInflightMsgs: 256, Logger: &etcdraft.DefaultLogger{Logger: log.New(io.Discard, "", 0)}})
	require.NoError(t, err)
	require.NoError(t, raw.Campaign())
	ready := raw.Ready()
	require.NoError(t, storage.Append(ready.Entries))
	require.NoError(t, storage.SetHardState(ready.HardState))
	raw.Advance(ready)
	require.Equal(t, etcdraft.StateLeader, raw.BasicStatus().RaftState)
	// Leave a real configuration entry pending. A second configuration proposal
	// is rewritten in place by etcd Raft, including its shared Entries backing.
	require.NoError(t, raw.ProposeConfChange(raftpb.ConfChange{Type: raftpb.ConfChangeAddLearnerNode, NodeID: 2}))
	data, err := (&raftpb.ConfChange{Type: raftpb.ConfChangeAddLearnerNode, NodeID: 3, Context: make([]byte, (4<<20)+1024)}).Marshal()
	require.NoError(t, err)
	msg := raftpb.Message{Type: raftpb.MsgProp, From: 1, To: 1, Entries: []raftpb.Entry{{Type: raftpb.EntryConfChange, Data: data}}}
	require.Greater(t, uint64(msg.Size()), maxReadyInputBytes)
	charged, err := stepRaftInput(raw, msg)
	require.NoError(t, err)
	require.Empty(t, msg.Entries[0].Data, "fixture did not exercise real Raft configuration rewriting")
	require.Equal(t, raftpb.EntryNormal, msg.Entries[0].Type)
	require.Less(t, uint64(msg.Size()), maxReadyInputBytes)
	require.Greater(t, charged, maxReadyInputBytes, "an oversized received input escaped the original-byte fairness cut")
}
