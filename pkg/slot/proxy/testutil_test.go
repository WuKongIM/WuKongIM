package proxy

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/hashslot"
	raftstorage "github.com/WuKongIM/WuKongIM/pkg/raftlog"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
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

	db, err := raftstorage.Open(path, raftstorage.Options{})
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
	cluster *proxyTestCluster
	store   *Store
	db      *metadb.DB
	nodeID  multiraft.NodeID
}

func startTwoNodeShardedStores(t testing.TB) []*testStoreNode {
	t.Helper()

	return startTwoNodeHashSlotStores(t, 2)
}

func startTwoNodeHashSlotStores(t testing.TB, hashSlotCount uint16) []*testStoreNode {
	t.Helper()

	layout := newProxyTestLayout(2, hashSlotCount)
	leaders := map[multiraft.SlotID]multiraft.NodeID{
		1: 1,
		2: 2,
	}
	peers := map[multiraft.SlotID][]multiraft.NodeID{
		1: {1},
		2: {2},
	}
	registry := make(map[multiraft.NodeID]*proxyTestCluster, 2)
	out := make([]*testStoreNode, 2)
	for i := range 2 {
		dir := filepath.Join(t.TempDir(), fmt.Sprintf("hs-n%d", i+1))
		db := openTestDBAt(t, filepath.Join(dir, "biz"))
		nodeID := multiraft.NodeID(i + 1)
		cluster := newProxyTestCluster(t, db, nodeID, layout, leaders, peers, registry)
		registry[nodeID] = cluster

		out[i] = &testStoreNode{
			cluster: cluster,
			store:   newProxyTestStore(cluster, db),
			db:      db,
			nodeID:  nodeID,
		}
	}

	for slotID := uint64(1); slotID <= 2; slotID++ {
		waitForExpectedSlotLeader(t, out, multiraft.SlotID(slotID), multiraft.NodeID(slotID))
	}

	return out
}

func newSingleNodeProxyTestStore(t testing.TB, db *metadb.DB, slotCount int) (*proxyTestCluster, *Store) {
	t.Helper()

	layout := newProxyTestLayout(slotCount, uint16(slotCount))
	leaders := make(map[multiraft.SlotID]multiraft.NodeID, slotCount)
	peers := make(map[multiraft.SlotID][]multiraft.NodeID, slotCount)
	for _, slotID := range layout.slots {
		leaders[slotID] = 1
		peers[slotID] = []multiraft.NodeID{1}
	}
	registry := make(map[multiraft.NodeID]*proxyTestCluster, 1)
	cluster := newProxyTestCluster(t, db, 1, layout, leaders, peers, registry)
	registry[1] = cluster
	return cluster, newProxyTestStore(cluster, db)
}

func newSingleNodeNoLeaderProxyTestStore(t testing.TB, db *metadb.DB, slotCount int) (*proxyTestCluster, *Store) {
	t.Helper()

	layout := newProxyTestLayout(slotCount, uint16(slotCount))
	peers := make(map[multiraft.SlotID][]multiraft.NodeID, slotCount)
	for _, slotID := range layout.slots {
		peers[slotID] = []multiraft.NodeID{1}
	}
	registry := make(map[multiraft.NodeID]*proxyTestCluster, 1)
	cluster := newProxyTestCluster(t, db, 1, layout, nil, peers, registry)
	registry[1] = cluster
	return cluster, newProxyTestStore(cluster, db)
}

func newProxyTestStore(cluster *proxyTestCluster, db *metadb.DB) *Store {
	store := New(cluster, db)
	if cluster != nil {
		store.RegisterRPCHandlers(cluster.registerRPCHandler)
	}
	return store
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

func findChannelIDForSlot(t testing.TB, cluster *proxyTestCluster, slot uint64, prefix string) string {
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

func findChannelIDForSlotWithDifferentHashSlot(t testing.TB, cluster *proxyTestCluster, slot uint64, hashSlot uint16, prefix string) string {
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

func findUIDForSlot(t testing.TB, cluster *proxyTestCluster, slot uint64, prefix string) string {
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

func findUIDForSlotWithDifferentHashSlot(t testing.TB, cluster *proxyTestCluster, slot uint64, hashSlot uint16, prefix string) string {
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

func mustHashSlotForKey(t testing.TB, cluster *proxyTestCluster, key string) uint16 {
	t.Helper()
	return hashSlotForKey(cluster, key)
}

type proxyTestLayout struct {
	slots          []multiraft.SlotID
	hashSlotCount  uint16
	hashSlotToSlot map[uint16]multiraft.SlotID
	slotHashSlots  map[multiraft.SlotID][]uint16
	version        uint64
}

func newProxyTestLayout(slotCount int, hashSlotCount uint16) proxyTestLayout {
	if slotCount <= 0 {
		slotCount = 1
	}
	if hashSlotCount == 0 {
		hashSlotCount = uint16(slotCount)
	}
	table := hashslot.NewHashSlotTable(hashSlotCount, slotCount)
	layout := proxyTestLayout{
		slots:          make([]multiraft.SlotID, 0, slotCount),
		hashSlotCount:  table.HashSlotCount(),
		hashSlotToSlot: make(map[uint16]multiraft.SlotID, hashSlotCount),
		slotHashSlots:  make(map[multiraft.SlotID][]uint16, slotCount),
		version:        table.Version(),
	}
	for slotID := 1; slotID <= slotCount; slotID++ {
		id := multiraft.SlotID(slotID)
		layout.slots = append(layout.slots, id)
		layout.slotHashSlots[id] = append([]uint16(nil), table.HashSlotsOf(id)...)
		for _, hashSlot := range layout.slotHashSlots[id] {
			layout.hashSlotToSlot[hashSlot] = id
		}
	}
	return layout
}

type proxyTestCluster struct {
	mu            sync.RWMutex
	applyMu       sync.Mutex
	nodeID        multiraft.NodeID
	layout        proxyTestLayout
	leaders       map[multiraft.SlotID]multiraft.NodeID
	peers         map[multiraft.SlotID][]multiraft.NodeID
	nodes         map[multiraft.NodeID]*proxyTestCluster
	stateMachines map[multiraft.SlotID]multiraft.StateMachine
	nextIndex     map[multiraft.SlotID]uint64
	handlers      map[uint8]func(context.Context, []byte) ([]byte, error)
}

func newProxyTestCluster(t testing.TB, db *metadb.DB, nodeID multiraft.NodeID, layout proxyTestLayout, leaders map[multiraft.SlotID]multiraft.NodeID, peers map[multiraft.SlotID][]multiraft.NodeID, registry map[multiraft.NodeID]*proxyTestCluster) *proxyTestCluster {
	t.Helper()

	cluster := &proxyTestCluster{
		nodeID:        nodeID,
		layout:        layout,
		leaders:       cloneLeaderMap(leaders),
		peers:         clonePeerMap(peers),
		nodes:         registry,
		stateMachines: make(map[multiraft.SlotID]multiraft.StateMachine, len(layout.slots)),
		nextIndex:     make(map[multiraft.SlotID]uint64, len(layout.slots)),
		handlers:      make(map[uint8]func(context.Context, []byte) ([]byte, error)),
	}
	for _, slotID := range layout.slots {
		sm, err := metafsm.NewStateMachineWithHashSlots(db, uint64(slotID), layout.slotHashSlots[slotID])
		if err != nil {
			t.Fatalf("NewStateMachineWithHashSlots(slot=%d) error = %v", slotID, err)
		}
		cluster.stateMachines[slotID] = sm
	}
	return cluster
}

func cloneLeaderMap(in map[multiraft.SlotID]multiraft.NodeID) map[multiraft.SlotID]multiraft.NodeID {
	out := make(map[multiraft.SlotID]multiraft.NodeID, len(in))
	for slotID, leaderID := range in {
		out[slotID] = leaderID
	}
	return out
}

func clonePeerMap(in map[multiraft.SlotID][]multiraft.NodeID) map[multiraft.SlotID][]multiraft.NodeID {
	out := make(map[multiraft.SlotID][]multiraft.NodeID, len(in))
	for slotID, peers := range in {
		out[slotID] = append([]multiraft.NodeID(nil), peers...)
	}
	return out
}

func (c *proxyTestCluster) NodeID() multiraft.NodeID {
	if c == nil {
		return 0
	}
	return c.nodeID
}

func (c *proxyTestCluster) SlotIDs() []multiraft.SlotID {
	if c == nil {
		return nil
	}
	return append([]multiraft.SlotID(nil), c.layout.slots...)
}

func (c *proxyTestCluster) SlotForKey(key string) multiraft.SlotID {
	if c == nil {
		return 0
	}
	return c.layout.hashSlotToSlot[c.HashSlotForKey(key)]
}

func (c *proxyTestCluster) HashSlotForKey(key string) uint16 {
	if c == nil {
		return 0
	}
	return hashslot.HashSlotForKey(key, c.layout.hashSlotCount)
}

func (c *proxyTestCluster) HashSlotsOf(slotID multiraft.SlotID) []uint16 {
	if c == nil {
		return nil
	}
	return append([]uint16(nil), c.layout.slotHashSlots[slotID]...)
}

func (c *proxyTestCluster) HashSlotTableVersion() uint64 {
	if c == nil {
		return 0
	}
	return c.layout.version
}

func (c *proxyTestCluster) LeaderOf(slotID multiraft.SlotID) (multiraft.NodeID, error) {
	if c == nil {
		return 0, ErrSlotNotFound
	}
	if _, ok := c.layout.slotHashSlots[slotID]; !ok {
		return 0, ErrSlotNotFound
	}
	c.mu.RLock()
	leaderID, ok := c.leaders[slotID]
	c.mu.RUnlock()
	if !ok || leaderID == 0 {
		return 0, ErrNoLeader
	}
	return leaderID, nil
}

func (c *proxyTestCluster) IsLocal(nodeID multiraft.NodeID) bool {
	return c != nil && nodeID == c.nodeID
}

func (c *proxyTestCluster) PeersForSlot(slotID multiraft.SlotID) []multiraft.NodeID {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	peers := append([]multiraft.NodeID(nil), c.peers[slotID]...)
	c.mu.RUnlock()
	return peers
}

func (c *proxyTestCluster) RPCService(ctx context.Context, nodeID multiraft.NodeID, slotID multiraft.SlotID, serviceID uint8, payload []byte) ([]byte, error) {
	if c == nil {
		return nil, ErrSlotNotFound
	}
	if _, ok := c.layout.slotHashSlots[slotID]; !ok {
		return nil, ErrSlotNotFound
	}
	c.mu.RLock()
	target := c.nodes[nodeID]
	c.mu.RUnlock()
	if target == nil {
		return nil, ErrNoLeader
	}
	target.mu.RLock()
	handler := target.handlers[serviceID]
	target.mu.RUnlock()
	if handler == nil {
		return nil, fmt.Errorf("missing rpc handler %d", serviceID)
	}
	return handler(ctx, append([]byte(nil), payload...))
}

func (c *proxyTestCluster) ProposeWithHashSlot(ctx context.Context, slotID multiraft.SlotID, hashSlot uint16, cmd []byte) error {
	result, err := c.ProposeWithHashSlotResult(ctx, slotID, hashSlot, cmd)
	if err != nil {
		return err
	}
	return proxyTestApplyResultError(cmd, result)
}

func (c *proxyTestCluster) ProposeLocalWithHashSlot(ctx context.Context, slotID multiraft.SlotID, hashSlot uint16, cmd []byte) error {
	leaderID, err := c.LeaderOf(slotID)
	if err != nil {
		return err
	}
	if !c.IsLocal(leaderID) {
		return ErrNotLeader
	}
	return c.ProposeWithHashSlot(ctx, slotID, hashSlot, cmd)
}

func (c *proxyTestCluster) ProposeWithHashSlotResult(ctx context.Context, slotID multiraft.SlotID, hashSlot uint16, cmd []byte) ([]byte, error) {
	leaderID, err := c.LeaderOf(slotID)
	if err != nil {
		return nil, err
	}
	c.mu.RLock()
	leader := c.nodes[leaderID]
	c.mu.RUnlock()
	if leader == nil {
		return nil, ErrNoLeader
	}
	sm := leader.stateMachines[slotID]
	if sm == nil {
		return nil, ErrSlotNotFound
	}

	leader.applyMu.Lock()
	defer leader.applyMu.Unlock()
	leader.nextIndex[slotID]++
	return sm.Apply(ctx, multiraft.Command{
		SlotID:   slotID,
		HashSlot: hashSlot,
		Index:    leader.nextIndex[slotID],
		Term:     1,
		Data:     append([]byte(nil), cmd...),
	})
}

func (c *proxyTestCluster) registerRPCHandler(serviceID uint8, handler func(context.Context, []byte) ([]byte, error)) {
	if c == nil || handler == nil {
		return
	}
	c.mu.Lock()
	c.handlers[serviceID] = handler
	c.mu.Unlock()
}

func proxyTestApplyResultError(command []byte, result []byte) error {
	switch string(result) {
	case metafsm.ApplyResultStaleMeta:
		return metadb.ErrStaleMeta
	}
	return nil
}
