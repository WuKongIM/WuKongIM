package raft

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controller/command"
	"github.com/WuKongIM/WuKongIM/pkg/controller/raft/raftstore"
	"github.com/WuKongIM/WuKongIM/pkg/controller/state"
	"github.com/stretchr/testify/require"
	"go.etcd.io/raft/v3/raftpb"
)

func TestServiceStartRebuildsMissingMaterializedStateFromCommittedWAL(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	raftDir := filepath.Join(dir, "controller-raft")
	statePath := filepath.Join(dir, "cluster-state.json")
	peers := []Peer{{NodeID: 1, Addr: "n1"}}

	initCommand := testInitCommand("wk-recover-missing-state", peers)
	renameCommand := recoveryUpsertNodeCommand(1, 1, "node-1-recovered")
	seedRecoveryWAL(t, raftDir, []raftpb.Entry{
		recoveryConfChangeEntry(t, 1, 1),
		recoveryCommandEntry(t, 2, initCommand),
		recoveryCommandEntry(t, 3, renameCommand),
	}, 3, 3)

	service, err := NewService(Config{
		NodeID:         1,
		Peers:          peers,
		AllowBootstrap: false,
		RaftDir:        raftDir,
		StateMachine:   newTestStateMachine(t, statePath),
		Transport:      discardRecoveryTransport{},
		TickInterval:   time.Hour,
	})
	require.NoError(t, err)
	require.NoError(t, service.Start(ctx))
	t.Cleanup(func() { require.NoError(t, service.Stop()) })

	recovered := service.cfg.StateMachine.Snapshot(ctx)
	require.Equal(t, uint64(2), recovered.Revision)
	require.Equal(t, uint64(3), recovered.AppliedRaftIndex)
	require.Equal(t, "node-1-recovered", recoveryNodeName(t, recovered, 1))
}

func TestServiceStartFailsClosedWhenStateAndCompactedSnapshotAreMissing(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	raftDir := filepath.Join(dir, "controller-raft")
	statePath := filepath.Join(dir, "cluster-state.json")
	peers := []Peer{{NodeID: 1, Addr: "n1"}}
	entries := []raftpb.Entry{
		recoveryConfChangeEntry(t, 1, 1),
		recoveryCommandEntry(t, 2, testInitCommand("wk-recover-compacted", peers)),
		recoveryCommandEntry(t, 3, recoveryUpsertNodeCommand(1, 1, "node-1-after-snapshot")),
	}
	store, err := raftstore.Open(ctx, raftstore.Config{Dir: raftDir, NodeID: 1, SegmentSize: 1 << 20})
	require.NoError(t, err)
	require.NoError(t, store.SaveReady(ctx, raftpb.HardState{Term: 1, Vote: 1, Commit: 3}, entries, raftpb.Snapshot{}))
	require.NoError(t, store.MarkAppliedBatch(ctx, 3))
	require.NoError(t, store.SaveSnapshot(ctx, raftpb.Snapshot{
		Data: []byte("snapshot-will-be-removed"),
		Metadata: raftpb.SnapshotMetadata{
			Index: 2,
			Term:  1,
			ConfState: raftpb.ConfState{
				Voters: []uint64{1},
			},
		},
	}))
	require.NoError(t, store.Close())

	snapshots, err := filepath.Glob(filepath.Join(raftDir, "snap", "*.snap"))
	require.NoError(t, err)
	require.Len(t, snapshots, 1)
	require.NoError(t, os.Remove(snapshots[0]))

	service, err := NewService(Config{
		NodeID:         1,
		Peers:          peers,
		AllowBootstrap: false,
		RaftDir:        raftDir,
		StateMachine:   newTestStateMachine(t, statePath),
		Transport:      discardRecoveryTransport{},
		TickInterval:   time.Hour,
	})
	require.NoError(t, err)
	err = service.Start(ctx)
	if err == nil {
		require.NoError(t, service.Stop())
	}
	require.Error(t, err)
	require.Contains(t, err.Error(), "snapshot")
}

func TestServiceStartRepairsOnlyIncompleteNewestWALTail(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	raftDir := filepath.Join(dir, "controller-raft")
	statePath := filepath.Join(dir, "cluster-state.json")
	peers := []Peer{{NodeID: 1, Addr: "n1"}}
	initCommand := testInitCommand("wk-repair-tail", peers)
	seedRecoveryWAL(t, raftDir, []raftpb.Entry{
		recoveryConfChangeEntry(t, 1, 1),
		recoveryCommandEntry(t, 2, initCommand),
	}, 2, 2)
	materialized := newTestStateMachine(t, statePath)
	_, err := materialized.Apply(ctx, 2, initCommand)
	require.NoError(t, err)

	tail := recoveryWALTail(t, raftDir)
	before, err := os.Stat(tail)
	require.NoError(t, err)
	f, err := os.OpenFile(tail, os.O_APPEND|os.O_WRONLY, 0)
	require.NoError(t, err)
	_, err = f.Write([]byte{0, 0, 0})
	require.NoError(t, err)
	require.NoError(t, f.Close())

	service := newRecoveryService(t, peers, raftDir, statePath)
	require.NoError(t, service.Start(ctx))
	t.Cleanup(func() { require.NoError(t, service.Stop()) })
	after, err := os.Stat(tail)
	require.NoError(t, err)
	require.Equal(t, before.Size(), after.Size())
	require.Equal(t, uint64(1), service.cfg.StateMachine.Snapshot(ctx).Revision)
}

func TestServiceStartFailsClosedWithoutRewritingWALChecksumCorruption(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	raftDir := filepath.Join(dir, "controller-raft")
	statePath := filepath.Join(dir, "cluster-state.json")
	peers := []Peer{{NodeID: 1, Addr: "n1"}}
	seedRecoveryWAL(t, raftDir, []raftpb.Entry{
		recoveryConfChangeEntry(t, 1, 1),
		recoveryCommandEntry(t, 2, testInitCommand("wk-corrupt-tail", peers)),
	}, 2, 0)

	tail := recoveryWALTail(t, raftDir)
	corrupted, err := os.ReadFile(tail)
	require.NoError(t, err)
	corrupted[len(corrupted)-1] ^= 0xff
	require.NoError(t, os.WriteFile(tail, corrupted, 0o644))

	service := newRecoveryService(t, peers, raftDir, statePath)
	err = service.Start(ctx)
	require.ErrorIs(t, err, raftstore.ErrCRCMismatch)
	require.True(t, service.Status().Degraded)
	after, readErr := os.ReadFile(tail)
	require.NoError(t, readErr)
	require.Equal(t, corrupted, after)
}

type discardRecoveryTransport struct{}

func (discardRecoveryTransport) Send([]raftpb.Message) {}

func newRecoveryService(t *testing.T, peers []Peer, raftDir, statePath string) *Service {
	t.Helper()
	service, err := NewService(Config{
		NodeID:         1,
		Peers:          peers,
		AllowBootstrap: false,
		RaftDir:        raftDir,
		StateMachine:   newTestStateMachine(t, statePath),
		Transport:      discardRecoveryTransport{},
		TickInterval:   time.Hour,
	})
	require.NoError(t, err)
	return service
}

func recoveryWALTail(t *testing.T, raftDir string) string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(raftDir, "wal", "*.wal"))
	require.NoError(t, err)
	require.NotEmpty(t, files)
	return files[len(files)-1]
}

func seedRecoveryWAL(t *testing.T, dir string, entries []raftpb.Entry, commit, applied uint64) {
	t.Helper()
	store, err := raftstore.Open(context.Background(), raftstore.Config{Dir: dir, NodeID: 1, SegmentSize: 1 << 20})
	require.NoError(t, err)
	require.NoError(t, store.SaveReady(context.Background(), raftpb.HardState{Term: 1, Vote: 1, Commit: commit}, entries, raftpb.Snapshot{}))
	if applied > 0 {
		require.NoError(t, store.MarkAppliedBatch(context.Background(), applied))
	}
	require.NoError(t, store.Close())
}

func recoveryConfChangeEntry(t *testing.T, index, nodeID uint64) raftpb.Entry {
	t.Helper()
	data, err := (&raftpb.ConfChange{Type: raftpb.ConfChangeAddNode, NodeID: nodeID}).Marshal()
	require.NoError(t, err)
	return raftpb.Entry{Type: raftpb.EntryConfChange, Term: 1, Index: index, Data: data}
}

func recoveryCommandEntry(t *testing.T, index uint64, cmd command.Command) raftpb.Entry {
	t.Helper()
	data, err := command.Encode(cmd)
	require.NoError(t, err)
	return raftpb.Entry{Type: raftpb.EntryNormal, Term: 1, Index: index, Data: data}
}

func recoveryUpsertNodeCommand(expectedRevision, nodeID uint64, name string) command.Command {
	node := state.Node{
		NodeID: nodeID, Name: name, Addr: fmt.Sprintf("n%d", nodeID),
		Roles:     []state.NodeRole{state.NodeRoleControllerVoter, state.NodeRoleData},
		JoinState: state.NodeJoinStateActive, Status: state.NodeStatusAlive, CapacityWeight: 10,
	}
	return command.Command{Kind: command.KindUpsertNode, ExpectedRevision: &expectedRevision, Node: &node}
}

func recoveryNodeName(t *testing.T, st state.ClusterState, nodeID uint64) string {
	t.Helper()
	for _, node := range st.Nodes {
		if node.NodeID == nodeID {
			return node.Name
		}
	}
	t.Fatalf("node %d not found", nodeID)
	return ""
}
