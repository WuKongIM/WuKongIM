//go:build integration

package raft

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controller/fsm"
	"github.com/WuKongIM/WuKongIM/pkg/controller/state"
	"github.com/WuKongIM/WuKongIM/pkg/controller/statefile"
	"github.com/stretchr/testify/require"
)

// checkpointNode is one single-voter Controller Raft service whose state file
// is a checkpoint (issue #1098): it is rarely written while running.
type checkpointNode struct {
	dir, raftDir, statePath string
	peers                   []Peer
	catchUpEntries          uint64
	sm                      *fsm.StateMachine
	service                 *Service
}

func (n *checkpointNode) start(t *testing.T) {
	t.Helper()
	sm, err := fsm.New(statefile.New(n.statePath), fsm.WithCheckpointPolicy(1<<20, time.Hour))
	require.NoError(t, err)
	service, err := NewService(Config{NodeID: 1, Peers: n.peers, AllowBootstrap: true, RaftDir: n.raftDir, StateMachine: sm, Transport: newMemoryRaftTransport(), TickInterval: testRaftTickInterval, SnapshotCatchUpEntries: n.catchUpEntries})
	require.NoError(t, err)
	require.NoError(t, service.Start(context.Background()))
	n.sm, n.service = sm, service
	t.Cleanup(func() { _ = service.Stop() })
	require.Eventually(t, func() bool { return service.Status().Role == RoleLeader }, 5*time.Second, 10*time.Millisecond)
}

func (n *checkpointNode) upserts(t *testing.T, names ...string) {
	t.Helper()
	ctx := context.Background()
	for _, name := range names {
		rev := n.sm.Snapshot(ctx).Revision
		_, err := n.service.ProposeResult(ctx, testUpsertNodeCommand(rev, 1, name))
		require.NoError(t, err)
	}
}

func readStateFile(t *testing.T, path string) state.ClusterState {
	t.Helper()
	st, err := statefile.New(path).Load(context.Background())
	require.NoError(t, err)
	return st
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	data, err := os.ReadFile(from)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(to, data, 0o600))
}

func initCheckpointNode(t *testing.T) *checkpointNode {
	t.Helper()
	return initCheckpointNodeWithCatchUp(t, 0)
}

func initCheckpointNodeWithCatchUp(t *testing.T, catchUp uint64) *checkpointNode {
	t.Helper()
	dir := t.TempDir()
	n := &checkpointNode{dir: dir, raftDir: filepath.Join(dir, "controller-raft"), statePath: filepath.Join(dir, "cluster-state.json"), peers: []Peer{{NodeID: 1, Addr: "n1"}}, catchUpEntries: catchUp}
	n.start(t)
	ctx := context.Background()
	_, err := n.service.ProposeResult(ctx, testInitCommand("wk-checkpoint", n.peers))
	require.NoError(t, err)
	return n
}

// A clean stop checkpoints the latest applied state, and while running the
// state file really lags, proving the hot path no longer saves every batch.
func TestCheckpointModeCleanStopPersistsLatestState(t *testing.T) {
	n := initCheckpointNode(t)
	n.upserts(t, "a", "b", "c", "d")
	want := n.sm.Snapshot(context.Background())
	require.Less(t, readStateFile(t, n.statePath).AppliedRaftIndex, want.AppliedRaftIndex, "checkpoint mode must not save every batch")

	require.NoError(t, n.service.Stop())
	require.Equal(t, want.AppliedRaftIndex, readStateFile(t, n.statePath).AppliedRaftIndex)

	n.start(t)
	got := n.sm.Snapshot(context.Background())
	require.Equal(t, want.Revision, got.Revision)
	require.Equal(t, want.Checksum, got.Checksum)
}

// F3/F5: a crash before later checkpoints leaves an old state file and stale
// metadata; restart must replay the committed WAL to the identical state.
func TestCheckpointModeRestartFromOldCheckpointReplaysWAL(t *testing.T) {
	n := initCheckpointNode(t)
	oldState := filepath.Join(n.dir, "old-cluster-state.json")
	oldMeta := filepath.Join(n.dir, "old-meta.json")
	copyFile(t, n.statePath, oldState)
	copyFile(t, filepath.Join(n.raftDir, "meta.json"), oldMeta)
	n.upserts(t, "a", "b", "c", "replayed")
	want := n.sm.Snapshot(context.Background())
	require.NoError(t, n.service.Stop())

	copyFile(t, oldState, n.statePath)
	copyFile(t, oldMeta, filepath.Join(n.raftDir, "meta.json"))
	require.Less(t, readStateFile(t, n.statePath).AppliedRaftIndex, want.AppliedRaftIndex)

	n.start(t)
	got := n.sm.Snapshot(context.Background())
	require.Equal(t, want.Revision, got.Revision)
	require.Equal(t, want.AppliedRaftIndex, got.AppliedRaftIndex)
	require.Equal(t, want.Checksum, got.Checksum)
}

// F4: before compaction releases WAL history, the state file must reach the
// snapshot boundary, so even recovery that never restores from a Raft
// snapshot (pre-v3.0.0-beta.20) can replay from the state file.
func TestCompactionCheckpointsStateFileBeforeReleasingWAL(t *testing.T) {
	const catchUp = 1
	n := initCheckpointNodeWithCatchUp(t, catchUp)
	n.upserts(t, "a", "b", "c", "d", "e")
	result, err := n.service.CompactLog(context.Background())
	require.NoError(t, err)
	require.True(t, result.Compacted, "result=%+v", result)
	require.Greater(t, result.AfterSnapshotIndex, uint64(catchUp), "WAL history must actually be released")

	// Read while running: Stop's own checkpoint must not mask a missing one.
	require.GreaterOrEqual(t, readStateFile(t, n.statePath).AppliedRaftIndex, result.AfterSnapshotIndex-catchUp)
}

func raftDirFingerprint(t *testing.T, dir string) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	require.NoError(t, filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			out[p] = info.Size()
		}
		return err
	}))
	return out
}

// A run that fails on its own still performs its durable shutdown
// (checkpoints, WAL close). Stop must join that work: nothing may write the
// Raft directory after Stop returns, or a restart could race the old owner.
func TestStopAfterRunFailureJoinsDurableShutdown(t *testing.T) {
	c, gate := newReadBarrierGatedCluster(t)
	node := c.nodes[0]
	block := gate.holdNext(errors.New("checkpoint fixture: injected apply failure"))
	proposal := startReadBarrierProposal(t, node, testUpsertNodeCommand(1, 1, "fails"))
	waitReadBarrierSignal(t, block.entered, "command never reached the FSM port")
	block.release()
	require.Error(t, proposal.wait(t))

	require.NoError(t, node.service.Stop())
	before := raftDirFingerprint(t, node.raftDir)
	time.Sleep(300 * time.Millisecond)
	require.Equal(t, before, raftDirFingerprint(t, node.raftDir), "the failed run wrote after Stop returned")
}
