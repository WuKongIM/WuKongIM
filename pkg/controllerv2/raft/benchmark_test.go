package raft

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/command"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/fsm"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/raft/raftstore"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/state"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/statefile"
	"github.com/stretchr/testify/require"
	"go.etcd.io/raft/v3/raftpb"
)

func BenchmarkControllerRaftProposeSingleNode(b *testing.B) {
	cluster := newRaftBenchmarkCluster(b, []uint64{1})
	cluster.start(b)
	cluster.propose(b, testInitCommand("bench-single", cluster.peers))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cluster.propose(b, testUpsertNodeCommand(uint64(i+1), 1, fmt.Sprintf("node-%d", i)))
	}
}

func BenchmarkControllerRaftStartupWithLongHistory(b *testing.B) {
	peers := []Peer{{NodeID: 1, Addr: "n1"}}
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		dir := b.TempDir()
		raftDir := filepath.Join(dir, "controller-raft")
		statePath := filepath.Join(dir, "cluster-state.json")
		seedBenchmarkSnapshotAndSuffix(b, raftDir, 100_000, 1_000)

		b.StartTimer()
		service := startSingleServiceForBenchmark(b, 1, peers, raftDir, statePath, false)
		b.StopTimer()

		require.NoError(b, service.Stop())
	}
}

type raftBenchmarkCluster struct {
	peers     []Peer
	nodes     []*raftBenchmarkNode
	transport *memoryRaftTransport
}

type raftBenchmarkNode struct {
	id        uint64
	raftDir   string
	statePath string
	service   *Service
}

func newRaftBenchmarkCluster(tb testing.TB, ids []uint64) *raftBenchmarkCluster {
	tb.Helper()
	transport := newMemoryRaftTransport()
	peers := make([]Peer, 0, len(ids))
	for _, id := range ids {
		peers = append(peers, Peer{NodeID: id, Addr: fmt.Sprintf("n%d", id)})
	}
	cluster := &raftBenchmarkCluster{peers: peers, transport: transport}
	for _, id := range ids {
		dir := tb.TempDir()
		node := &raftBenchmarkNode{id: id, raftDir: filepath.Join(dir, "controller-raft"), statePath: filepath.Join(dir, "cluster-state.json")}
		node.service = newBenchmarkService(tb, id, peers, node.raftDir, node.statePath, transport, true)
		cluster.nodes = append(cluster.nodes, node)
	}
	tb.Cleanup(cluster.stop)
	return cluster
}

func (c *raftBenchmarkCluster) start(tb testing.TB) {
	tb.Helper()
	for _, node := range c.nodes {
		require.NoError(tb, node.service.Start(context.Background()))
	}
	c.waitForLeader(tb)
}

func (c *raftBenchmarkCluster) stop() {
	for _, node := range c.nodes {
		if node.service != nil {
			_ = node.service.Stop()
		}
	}
}

func (c *raftBenchmarkCluster) waitForLeader(tb testing.TB) *raftBenchmarkNode {
	tb.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var leader *raftBenchmarkNode
		for _, node := range c.nodes {
			if node.service.Status().Role == RoleLeader {
				if leader != nil {
					leader = nil
					break
				}
				leader = node
			}
		}
		if leader != nil {
			return leader
		}
		time.Sleep(time.Millisecond)
	}
	tb.Fatal("controller raft benchmark leader was not elected")
	return nil
}

func (c *raftBenchmarkCluster) propose(tb testing.TB, cmd command.Command) {
	tb.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		leader := c.waitForLeader(tb)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err := leader.service.Propose(ctx, cmd)
		cancel()
		if err == nil {
			return
		}
		lastErr = err
		if !errors.Is(err, ErrNotLeader) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	require.NoError(tb, lastErr)
}

func startSingleServiceForBenchmark(tb testing.TB, id uint64, peers []Peer, raftDir string, statePath string, allowBootstrap bool) *Service {
	tb.Helper()
	transport := newMemoryRaftTransport()
	service := newBenchmarkService(tb, id, peers, raftDir, statePath, transport, allowBootstrap)
	require.NoError(tb, service.Start(context.Background()))
	return service
}

func newBenchmarkService(tb testing.TB, id uint64, peers []Peer, raftDir string, statePath string, transport *memoryRaftTransport, allowBootstrap bool) *Service {
	tb.Helper()
	sm, err := fsm.New(statefile.New(statePath))
	require.NoError(tb, err)
	service, err := NewService(Config{
		NodeID:         id,
		Peers:          peers,
		AllowBootstrap: allowBootstrap,
		RaftDir:        raftDir,
		StateMachine:   sm,
		Transport:      transport,
		TickInterval:   5 * time.Millisecond,
	})
	require.NoError(tb, err)
	transport.register(id, service)
	return service
}

func seedBenchmarkSnapshotAndSuffix(tb testing.TB, raftDir string, snapshotIndex uint64, suffixEntries uint64) {
	tb.Helper()
	ctx := context.Background()
	peers := []Peer{{NodeID: 1, Addr: "n1"}}
	snapshotData := benchmarkSnapshotData(tb, snapshotIndex, peers)
	store, err := raftstore.Open(ctx, raftstore.Config{Dir: raftDir, NodeID: 1, SegmentSize: 64 << 20})
	require.NoError(tb, err)
	defer func() { require.NoError(tb, store.Close()) }()

	snap := raftpb.Snapshot{
		Data: snapshotData,
		Metadata: raftpb.SnapshotMetadata{
			Index:     snapshotIndex,
			Term:      1,
			ConfState: raftpb.ConfState{Voters: []uint64{1}},
		},
	}
	require.NoError(tb, store.SaveSnapshot(ctx, snap))

	entries := make([]raftpb.Entry, 0, suffixEntries)
	for i := uint64(0); i < suffixEntries; i++ {
		cmd := testUpsertNodeCommand(i+1, 1, fmt.Sprintf("node-replay-%d", i))
		data, err := command.Encode(cmd)
		require.NoError(tb, err)
		entries = append(entries, raftpb.Entry{Index: snapshotIndex + i + 1, Term: 1, Type: raftpb.EntryNormal, Data: data})
	}
	hs := raftpb.HardState{Term: 1, Vote: 1, Commit: snapshotIndex + suffixEntries}
	require.NoError(tb, store.SaveReady(ctx, hs, entries, raftpb.Snapshot{}))
	require.NoError(tb, store.MarkAppliedBatch(ctx, snapshotIndex))
}

func benchmarkSnapshotData(tb testing.TB, snapshotIndex uint64, peers []Peer) []byte {
	tb.Helper()
	dir := tb.TempDir()
	sm, err := fsm.New(statefile.New(filepath.Join(dir, "cluster-state.json")))
	require.NoError(tb, err)
	_, err = sm.Apply(context.Background(), snapshotIndex, testInitCommand("bench-startup-history", peers))
	require.NoError(tb, err)
	st := sm.Snapshot(context.Background())
	st.AppliedRaftIndex = snapshotIndex
	data, err := state.Encode(st)
	require.NoError(tb, err)
	return data
}
