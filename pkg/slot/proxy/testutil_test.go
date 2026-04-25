package proxy

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	raftstorage "github.com/WuKongIM/WuKongIM/pkg/raftlog"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

const (
	testTickInterval = 10 * time.Millisecond
	testElectionTick = 10
	testWaitTimeout  = testTickInterval * time.Duration(testElectionTick*20)
	testPollInterval = testTickInterval
)

func mustNewStateMachine(t testing.TB, db *metadb.DB, slot uint64) multiraft.StateMachine {
	t.Helper()

	sm, err := metafsm.NewStateMachine(db, slot)
	if err != nil {
		t.Fatalf("NewStateMachine() error = %v", err)
	}
	return sm
}

func openTestDB(t testing.TB) *metadb.DB {
	t.Helper()

	return openTestDBAt(t, filepath.Join(t.TempDir(), "db"))
}

func openTestDBAt(t testing.TB, path string) *metadb.DB {
	t.Helper()

	db, err := metadb.Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		defer recoverDoubleClose()
		if err := db.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})
	return db
}

func openTestRaftDBAt(t testing.TB, path string) *raftstorage.DB {
	t.Helper()

	db, err := raftstorage.Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		defer recoverDoubleClose()
		if err := db.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})
	return db
}

// recoverDoubleClose catches panics from closing an already-closed database.
// The underlying storage engine (Pebble) may panic when Close is called
// on a DB that was already closed by the test. We detect this via
// isClosedPanic. All other panics are re-raised so real bugs surface.
func recoverDoubleClose() {
	r := recover()
	if r == nil {
		return
	}
	if isClosedPanic(r) {
		return
	}
	panic(r)
}

// isClosedPanic reports whether the recovered panic value indicates a
// double-close on an already-closed database.
func isClosedPanic(r any) bool {
	switch v := r.(type) {
	case error:
		return strings.Contains(v.Error(), "closed")
	case string:
		return strings.Contains(v, "closed")
	default:
		return false
	}
}

func newStartedRuntime(t testing.TB) *multiraft.Runtime {
	t.Helper()

	rt, err := multiraft.New(multiraft.Options{
		NodeID:       1,
		TickInterval: testTickInterval,
		Workers:      1,
		Transport:    fakeTransport{},
		Raft: multiraft.RaftOptions{
			ElectionTick:  testElectionTick,
			HeartbeatTick: 1,
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := rt.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})
	return rt
}

type fakeTransport struct{}

func (fakeTransport) Send(ctx context.Context, batch []multiraft.Envelope) error {
	return nil
}

func waitForCondition(t testing.TB, fn func() bool, msg string) {
	t.Helper()

	deadline := time.Now().Add(testWaitTimeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(testPollInterval)
	}
	t.Fatalf("condition not satisfied before timeout: %s", msg)
}

type testStoreNode struct {
	cluster *raftcluster.Cluster
	store   *Store
	db      *metadb.DB
	raftDB  *raftstorage.DB
	nodeID  multiraft.NodeID
}

func startTwoNodeShardedStores(t testing.TB) []*testStoreNode {
	t.Helper()

	listeners := make([]net.Listener, 2)
	for i := range 2 {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen %d: %v", i+1, err)
		}
		listeners[i] = ln
	}

	nodes := make([]raftcluster.NodeConfig, 2)
	for i := range 2 {
		nodes[i] = raftcluster.NodeConfig{
			NodeID: multiraft.NodeID(i + 1),
			Addr:   listeners[i].Addr().String(),
		}
		_ = listeners[i].Close()
	}

	slots := []raftcluster.SlotConfig{
		{SlotID: 1, Peers: []multiraft.NodeID{1}},
		{SlotID: 2, Peers: []multiraft.NodeID{2}},
	}

	out := make([]*testStoreNode, 2)
	for i := range 2 {
		dir := filepath.Join(t.TempDir(), fmt.Sprintf("n%d", i+1))
		db := openTestDBAt(t, filepath.Join(dir, "biz"))
		raftDB := openTestRaftDBAt(t, filepath.Join(dir, "raft"))

		cluster, err := raftcluster.NewCluster(raftcluster.Config{
			NodeID:             multiraft.NodeID(i + 1),
			ListenAddr:         nodes[i].Addr,
			SlotCount:          2,
			ControllerReplicaN: len(nodes),
			SlotReplicaN:       len(nodes),
			NewStorage: func(slotID multiraft.SlotID) (multiraft.Storage, error) {
				return raftDB.ForSlot(uint64(slotID)), nil
			},
			NewStateMachine:              metafsm.NewStateMachineFactory(db),
			NewStateMachineWithHashSlots: metafsm.NewHashSlotStateMachineFactory(db),
			Nodes:                        nodes,
			Slots:                        slots,
		})
		if err != nil {
			t.Fatalf("NewCluster(node=%d) error = %v", i+1, err)
		}
		require.NoError(t, cluster.Start())
		t.Cleanup(cluster.Stop)

		out[i] = &testStoreNode{
			cluster: cluster,
			store:   New(cluster, db),
			db:      db,
			raftDB:  raftDB,
			nodeID:  multiraft.NodeID(i + 1),
		}
	}

	for slotID := uint64(1); slotID <= 2; slotID++ {
		waitForExpectedSlotLeader(t, out, multiraft.SlotID(slotID), multiraft.NodeID(slotID))
	}

	return out
}

func startTwoNodeHashSlotStores(t testing.TB, hashSlotCount uint16) []*testStoreNode {
	t.Helper()

	listeners := make([]net.Listener, 2)
	for i := range 2 {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen %d: %v", i+1, err)
		}
		listeners[i] = ln
	}

	nodes := make([]raftcluster.NodeConfig, 2)
	for i := range 2 {
		nodes[i] = raftcluster.NodeConfig{
			NodeID: multiraft.NodeID(i + 1),
			Addr:   listeners[i].Addr().String(),
		}
		_ = listeners[i].Close()
	}

	slots := []raftcluster.SlotConfig{
		{SlotID: 1, Peers: []multiraft.NodeID{1}},
		{SlotID: 2, Peers: []multiraft.NodeID{2}},
	}

	out := make([]*testStoreNode, 2)
	for i := range 2 {
		dir := filepath.Join(t.TempDir(), fmt.Sprintf("hs-n%d", i+1))
		db := openTestDBAt(t, filepath.Join(dir, "biz"))
		raftDB := openTestRaftDBAt(t, filepath.Join(dir, "raft"))

		cluster, err := raftcluster.NewCluster(raftcluster.Config{
			NodeID:             multiraft.NodeID(i + 1),
			ListenAddr:         nodes[i].Addr,
			SlotCount:          2,
			HashSlotCount:      hashSlotCount,
			InitialSlotCount:   2,
			ControllerReplicaN: len(nodes),
			SlotReplicaN:       len(nodes),
			NewStorage: func(slotID multiraft.SlotID) (multiraft.Storage, error) {
				return raftDB.ForSlot(uint64(slotID)), nil
			},
			NewStateMachine:              metafsm.NewStateMachineFactory(db),
			NewStateMachineWithHashSlots: metafsm.NewHashSlotStateMachineFactory(db),
			Nodes:                        nodes,
			Slots:                        slots,
		})
		if err != nil {
			t.Fatalf("NewCluster(node=%d) error = %v", i+1, err)
		}
		require.NoError(t, cluster.Start())
		t.Cleanup(cluster.Stop)

		out[i] = &testStoreNode{
			cluster: cluster,
			store:   New(cluster, db),
			db:      db,
			raftDB:  raftDB,
			nodeID:  multiraft.NodeID(i + 1),
		}
	}

	for slotID := uint64(1); slotID <= 2; slotID++ {
		waitForExpectedSlotLeader(t, out, multiraft.SlotID(slotID), multiraft.NodeID(slotID))
	}

	return out
}

func waitForExpectedSlotLeader(t testing.TB, nodes []*testStoreNode, slotID multiraft.SlotID, want multiraft.NodeID) {
	t.Helper()

	waitForCondition(t, func() bool {
		for _, node := range nodes {
			if node == nil || node.cluster == nil {
				continue
			}
			leaderID, err := node.cluster.LeaderOf(slotID)
			if err == nil && leaderID == want {
				return true
			}
		}
		return false
	}, fmt.Sprintf("slot %d leader elected on expected node", slotID))
}

func findChannelIDForSlot(t testing.TB, cluster *raftcluster.Cluster, slot uint64, prefix string) string {
	t.Helper()

	for i := 0; i < 10_000; i++ {
		channelID := fmt.Sprintf("%s-%d", prefix, i)
		if uint64(cluster.SlotForKey(channelID)) == slot {
			return channelID
		}
	}
	t.Fatalf("no channel id found for slot %d", slot)
	return ""
}

func findChannelIDForSlotWithDifferentHashSlot(t testing.TB, cluster *raftcluster.Cluster, slot uint64, hashSlot uint16, prefix string) string {
	t.Helper()

	for i := 0; i < 10_000; i++ {
		channelID := fmt.Sprintf("%s-%d", prefix, i)
		if uint64(cluster.SlotForKey(channelID)) != slot {
			continue
		}
		if cluster.HashSlotForKey(channelID) == hashSlot {
			continue
		}
		return channelID
	}
	t.Fatalf("no channel id found for physical slot %d with hash slot != %d", slot, hashSlot)
	return ""
}

func findUIDForSlot(t testing.TB, cluster *raftcluster.Cluster, slot uint64, prefix string) string {
	t.Helper()

	for i := 0; i < 10_000; i++ {
		uid := fmt.Sprintf("%s-%d", prefix, i)
		if uint64(cluster.SlotForKey(uid)) == slot {
			return uid
		}
	}
	t.Fatalf("no uid found for slot %d", slot)
	return ""
}

func findUIDForSlotWithDifferentHashSlot(t testing.TB, cluster *raftcluster.Cluster, slot uint64, hashSlot uint16, prefix string) string {
	t.Helper()

	for i := 0; i < 10_000; i++ {
		uid := fmt.Sprintf("%s-%d", prefix, i)
		if uint64(cluster.SlotForKey(uid)) != slot {
			continue
		}
		if cluster.HashSlotForKey(uid) == hashSlot {
			continue
		}
		return uid
	}
	t.Fatalf("no uid found for physical slot %d with hash slot != %d", slot, hashSlot)
	return ""
}

func mustHashSlotForKey(t testing.TB, cluster *raftcluster.Cluster, key string) uint16 {
	t.Helper()
	return hashSlotForKey(cluster, key)
}
