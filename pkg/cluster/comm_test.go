package cluster_test

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"sort"
	"testing"
	"time"

	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	raftstorage "github.com/WuKongIM/WuKongIM/pkg/raftlog"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	metastore "github.com/WuKongIM/WuKongIM/pkg/slot/proxy"
	"github.com/stretchr/testify/require"
)

// testNode bundles a cluster, store, and storage resources for testing.
type testNode struct {
	cluster            *raftcluster.Cluster
	store              *metastore.Store
	db                 *metadb.DB
	raftDB             *raftstorage.DB
	nodeID             multiraft.NodeID
	dir                string
	listenAddr         string
	nodes              []raftcluster.NodeConfig
	slots              []raftcluster.SlotConfig
	slotCount          int
	hashSlotCount      uint16
	slotReplicaN       int
	controllerReplicaN int
	withController     bool
}

const (
	testClusterTickInterval    = 25 * time.Millisecond
	testClusterElectionTick    = 6
	testClusterHeartbeatTick   = 1
	testClusterDialTimeout     = 750 * time.Millisecond
	testClusterForwardTimeout  = 750 * time.Millisecond
	testClusterPoolSize        = 1
	testLeaderPollInterval     = 50 * time.Millisecond
	testLeaderConfirmations    = 4
	testManagedSlotProbeWait   = 300 * time.Millisecond
	testControllerProbeTimeout = 250 * time.Millisecond
)

func testClusterTimingConfig() raftcluster.Config {
	return raftcluster.Config{
		TickInterval:   testClusterTickInterval,
		ElectionTick:   testClusterElectionTick,
		HeartbeatTick:  testClusterHeartbeatTick,
		DialTimeout:    testClusterDialTimeout,
		ForwardTimeout: testClusterForwardTimeout,
		PoolSize:       testClusterPoolSize,
	}
}

func (n *testNode) stop() {
	if n == nil {
		return
	}
	if n.cluster != nil {
		n.cluster.Stop()
		n.cluster = nil
	}
	if n.raftDB != nil {
		_ = n.raftDB.Close()
		n.raftDB = nil
	}
	if n.db != nil {
		_ = n.db.Close()
		n.db = nil
	}
	n.store = nil
}

func newStartedTestNode(
	t testing.TB,
	dir string,
	nodeID multiraft.NodeID,
	listenAddr string,
	nodes []raftcluster.NodeConfig,
	slots []raftcluster.SlotConfig,
	slotCount int,
	slotReplicaN int,
	controllerReplicaN int,
	withController bool,
) *testNode {
	return newStartedTestNodeWithHashSlots(t, dir, nodeID, listenAddr, nodes, slots, slotCount, 0, slotReplicaN, controllerReplicaN, withController)
}

func newStartedTestNodeWithHashSlots(
	t testing.TB,
	dir string,
	nodeID multiraft.NodeID,
	listenAddr string,
	nodes []raftcluster.NodeConfig,
	slots []raftcluster.SlotConfig,
	slotCount int,
	hashSlotCount uint16,
	slotReplicaN int,
	controllerReplicaN int,
	withController bool,
) *testNode {
	t.Helper()

	db, err := metadb.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("open metadb node %d: %v", nodeID, err)
	}
	raftDB, err := raftstorage.Open(filepath.Join(dir, "raft"))
	if err != nil {
		_ = db.Close()
		t.Fatalf("open raftstorage node %d: %v", nodeID, err)
	}

	controllerMetaPath := ""
	controllerRaftPath := ""
	if withController {
		controllerMetaPath = filepath.Join(dir, "controller-meta")
		controllerRaftPath = filepath.Join(dir, "controller-raft")
	}

	cfg := raftcluster.Config{
		NodeID:             nodeID,
		ListenAddr:         listenAddr,
		SlotCount:          uint32(slotCount),
		HashSlotCount:      hashSlotCount,
		SlotReplicaN:       slotReplicaN,
		ControllerReplicaN: controllerReplicaN,
		NewStorage: func(slotID multiraft.SlotID) (multiraft.Storage, error) {
			return raftDB.ForSlot(uint64(slotID)), nil
		},
		NewStateMachine:              metafsm.NewStateMachineFactory(db),
		NewStateMachineWithHashSlots: metafsm.NewHashSlotStateMachineFactory(db),
		Nodes:                        append([]raftcluster.NodeConfig(nil), nodes...),
		Slots:                        append([]raftcluster.SlotConfig(nil), slots...),
		ControllerMetaPath:           controllerMetaPath,
		ControllerRaftPath:           controllerRaftPath,
		TickInterval:                 testClusterTickInterval,
		ElectionTick:                 testClusterElectionTick,
		HeartbeatTick:                testClusterHeartbeatTick,
		DialTimeout:                  testClusterDialTimeout,
		ForwardTimeout:               testClusterForwardTimeout,
		PoolSize:                     testClusterPoolSize,
	}

	c, err := raftcluster.NewCluster(cfg)
	if err != nil {
		_ = raftDB.Close()
		_ = db.Close()
		t.Fatalf("NewCluster node %d: %v", nodeID, err)
	}
	if err := c.Start(); err != nil {
		_ = raftDB.Close()
		_ = db.Close()
		t.Fatalf("Start node %d: %v", nodeID, err)
	}

	return &testNode{
		cluster:            c,
		store:              metastore.New(c, db),
		db:                 db,
		raftDB:             raftDB,
		nodeID:             nodeID,
		dir:                dir,
		listenAddr:         listenAddr,
		nodes:              append([]raftcluster.NodeConfig(nil), nodes...),
		slots:              append([]raftcluster.SlotConfig(nil), slots...),
		slotCount:          slotCount,
		hashSlotCount:      hashSlotCount,
		slotReplicaN:       slotReplicaN,
		controllerReplicaN: controllerReplicaN,
		withController:     withController,
	}
}

func startSingleNode(t testing.TB, slotCount int) *testNode {
	t.Helper()
	dir := t.TempDir()

	slots := make([]raftcluster.SlotConfig, slotCount)
	for i := range slotCount {
		slots[i] = raftcluster.SlotConfig{
			SlotID: multiraft.SlotID(i + 1),
			Peers:  []multiraft.NodeID{1},
		}
	}

	nodes := []raftcluster.NodeConfig{{NodeID: 1, Addr: "127.0.0.1:0"}}
	node := newStartedTestNode(t, dir, 1, "127.0.0.1:0", nodes, slots, slotCount, 1, 1, false)
	t.Cleanup(func() { stopNodes([]*testNode{node}) })
	return node
}

func startThreeNodes(t testing.TB, slotCount int) []*testNode {
	t.Helper()

	listeners := make([]net.Listener, 3)
	for i := range 3 {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen %d: %v", i, err)
		}
		listeners[i] = ln
	}

	nodes := make([]raftcluster.NodeConfig, 3)
	for i := range 3 {
		nodes[i] = raftcluster.NodeConfig{
			NodeID: multiraft.NodeID(i + 1),
			Addr:   listeners[i].Addr().String(),
		}
		listeners[i].Close()
	}

	slots := make([]raftcluster.SlotConfig, slotCount)
	for i := range slotCount {
		slots[i] = raftcluster.SlotConfig{
			SlotID: multiraft.SlotID(i + 1),
			Peers:  []multiraft.NodeID{1, 2, 3},
		}
	}

	testNodes := make([]*testNode, 3)
	root := t.TempDir()
	for i := range 3 {
		dir := filepath.Join(root, fmt.Sprintf("n%d", i+1))
		testNodes[i] = newStartedTestNode(
			t,
			dir,
			multiraft.NodeID(i+1),
			nodes[i].Addr,
			nodes,
			slots,
			slotCount,
			3,
			3,
			false,
		)
	}
	t.Cleanup(func() { stopNodes(testNodes) })

	return testNodes
}

func startSingleNodeWithController(t testing.TB, slotCount int, legacySlotCount int) *testNode {
	t.Helper()
	dir := t.TempDir()

	slots := make([]raftcluster.SlotConfig, legacySlotCount)
	for i := range legacySlotCount {
		slots[i] = raftcluster.SlotConfig{
			SlotID: multiraft.SlotID(i + 1),
			Peers:  []multiraft.NodeID{1},
		}
	}

	nodes := []raftcluster.NodeConfig{{NodeID: 1, Addr: "127.0.0.1:0"}}
	node := newStartedTestNode(t, dir, 1, "127.0.0.1:0", nodes, slots, slotCount, 1, 1, true)
	t.Cleanup(func() { stopNodes([]*testNode{node}) })
	return node
}

func startThreeNodesWithController(t testing.TB, slotCount int, legacyReplicaN int) []*testNode {
	return startThreeNodesWithControllerWithSettle(t, slotCount, legacyReplicaN, true)
}

func startThreeNodesWithControllerAndHashSlots(t testing.TB, slotCount int, hashSlotCount uint16, replicaN int) []*testNode {
	t.Helper()

	listeners := make([]net.Listener, 3)
	for i := range 3 {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen %d: %v", i, err)
		}
		listeners[i] = ln
	}

	nodes := make([]raftcluster.NodeConfig, 3)
	for i := range 3 {
		nodes[i] = raftcluster.NodeConfig{
			NodeID: multiraft.NodeID(i + 1),
			Addr:   listeners[i].Addr().String(),
		}
		listeners[i].Close()
	}

	testNodes := make([]*testNode, 3)
	root := t.TempDir()
	for i := range 3 {
		dir := filepath.Join(root, fmt.Sprintf("n%d", i+1))
		testNodes[i] = newStartedTestNodeWithHashSlots(
			t,
			dir,
			multiraft.NodeID(i+1),
			nodes[i].Addr,
			nodes,
			nil,
			slotCount,
			hashSlotCount,
			replicaN,
			3,
			true,
		)
	}
	t.Cleanup(func() { stopNodes(testNodes) })
	waitForManagedSlotsSettled(t, testNodes, slotCount)
	return testNodes
}

func startThreeNodesWithControllerWithSettle(t testing.TB, slotCount int, legacyReplicaN int, settle bool) []*testNode {
	t.Helper()

	listeners := make([]net.Listener, 3)
	for i := range 3 {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen %d: %v", i, err)
		}
		listeners[i] = ln
	}

	nodes := make([]raftcluster.NodeConfig, 3)
	for i := range 3 {
		nodes[i] = raftcluster.NodeConfig{
			NodeID: multiraft.NodeID(i + 1),
			Addr:   listeners[i].Addr().String(),
		}
		listeners[i].Close()
	}

	testNodes := make([]*testNode, 3)
	root := t.TempDir()
	for i := range 3 {
		dir := filepath.Join(root, fmt.Sprintf("n%d", i+1))
		testNodes[i] = newStartedTestNode(
			t,
			dir,
			multiraft.NodeID(i+1),
			nodes[i].Addr,
			nodes,
			nil,
			slotCount,
			legacyReplicaN,
			3,
			true,
		)
	}
	t.Cleanup(func() { stopNodes(testNodes) })
	if settle {
		waitForManagedSlotsSettled(t, testNodes, slotCount)
	}
	return testNodes
}

func startFourNodesWithController(t testing.TB, slotCount int, replicaN int) []*testNode {
	t.Helper()

	listeners := make([]net.Listener, 4)
	for i := range 4 {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen %d: %v", i, err)
		}
		listeners[i] = ln
	}

	nodes := make([]raftcluster.NodeConfig, 4)
	for i := range 4 {
		nodes[i] = raftcluster.NodeConfig{
			NodeID: multiraft.NodeID(i + 1),
			Addr:   listeners[i].Addr().String(),
		}
		listeners[i].Close()
	}

	testNodes := make([]*testNode, 4)
	root := t.TempDir()
	for i := range 4 {
		dir := filepath.Join(root, fmt.Sprintf("n%d", i+1))
		testNodes[i] = newStartedTestNode(
			t,
			dir,
			multiraft.NodeID(i+1),
			nodes[i].Addr,
			nodes,
			nil,
			slotCount,
			replicaN,
			3,
			true,
		)
	}
	t.Cleanup(func() { stopNodes(testNodes) })
	waitForManagedSlotsSettled(t, testNodes, slotCount)
	return testNodes
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

func startThreeOfFourNodesWithController(t testing.TB, slotCount int, replicaN int) []*testNode {
	t.Helper()

	listeners := make([]net.Listener, 4)
	for i := range 4 {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen %d: %v", i, err)
		}
		listeners[i] = ln
	}

	nodes := make([]raftcluster.NodeConfig, 4)
	for i := range 4 {
		nodes[i] = raftcluster.NodeConfig{
			NodeID: multiraft.NodeID(i + 1),
			Addr:   listeners[i].Addr().String(),
		}
		listeners[i].Close()
	}

	testNodes := make([]*testNode, 4)
	root := t.TempDir()
	for i := range 4 {
		dir := filepath.Join(root, fmt.Sprintf("n%d", i+1))
		if i < 3 {
			testNodes[i] = newStartedTestNode(
				t,
				dir,
				multiraft.NodeID(i+1),
				nodes[i].Addr,
				nodes,
				nil,
				slotCount,
				replicaN,
				3,
				true,
			)
			continue
		}
		testNodes[i] = &testNode{
			nodeID:             multiraft.NodeID(i + 1),
			dir:                dir,
			listenAddr:         nodes[i].Addr,
			nodes:              append([]raftcluster.NodeConfig(nil), nodes...),
			slotCount:          slotCount,
			slotReplicaN:       replicaN,
			controllerReplicaN: 3,
			withController:     true,
		}
	}
	t.Cleanup(func() { stopNodes(testNodes) })
	waitForManagedSlotsSettled(t, testNodes[:3], slotCount)
	return testNodes
}

func startFourNodesWithInjectedRepairFailure(t testing.TB, slotCount int, replicaN int) []*testNode {
	t.Helper()
	nodes := startFourNodesWithController(t, slotCount, replicaN)
	waitForStableLeader(t, assignedNodesForSlot(t, nodes, 1), 1)
	restore := setManagedSlotExecutionHookOnNodes(t, nodes, func(slotID uint32, task controllermeta.ReconcileTask) error {
		if slotID == 1 && task.Kind == controllermeta.TaskKindRepair {
			return errors.New("injected repair failure")
		}
		return nil
	})
	t.Cleanup(restore)
	requireControllerCommand(t, nodes, func(cluster *raftcluster.Cluster) error {
		return cluster.MarkNodeDraining(context.Background(), 2)
	})
	require.Eventually(t, func() bool {
		controller, ok := currentControllerLeaderNode(nodes)
		if !ok {
			return false
		}
		task, err := controller.cluster.GetReconcileTask(context.Background(), 1)
		return err == nil && task.Attempt >= 1
	}, 20*time.Second, 100*time.Millisecond)
	return nodes
}

func startFourNodesWithPermanentRepairFailure(t testing.TB, slotCount int, replicaN int) []*testNode {
	t.Helper()
	return startFourNodesWithInjectedRepairFailure(t, slotCount, replicaN)
}

func setManagedSlotExecutionHookOnNodes(t testing.TB, nodes []*testNode, hook raftcluster.ManagedSlotExecutionTestHook) func() {
	t.Helper()

	restores := make([]func(), 0, len(nodes))
	for _, node := range nodes {
		if node == nil || node.cluster == nil {
			continue
		}
		restores = append(restores, node.cluster.SetManagedSlotExecutionTestHook(hook))
	}
	return func() {
		for i := len(restores) - 1; i >= 0; i-- {
			restores[i]()
		}
	}
}

func stopNodes(nodes []*testNode) {
	stopped := false
	for _, node := range nodes {
		if node != nil && (node.cluster != nil || node.raftDB != nil || node.db != nil) {
			node.stop()
			stopped = true
		}
	}
	// Pebble-backed raft storage can still be finalizing the last batch commit
	// when the test temp dir cleanup runs. Give teardown a brief grace window.
	if stopped {
		time.Sleep(200 * time.Millisecond)
	}
}

func waitForControllerAssignments(t testing.TB, nodes []*testNode, slotCount int) {
	t.Helper()
	require.Eventually(t, func() bool {
		for _, node := range nodes {
			if node == nil || node.cluster == nil {
				continue
			}
			assignments, err := node.cluster.ListSlotAssignments(context.Background())
			if err == nil && len(assignments) == slotCount {
				return true
			}
		}
		return false
	}, 20*time.Second, 100*time.Millisecond)
}

func waitForManagedSlotsSettled(t testing.TB, nodes []*testNode, slotCount int) {
	t.Helper()
	waitForControllerAssignments(t, nodes, slotCount)

	require.Eventually(t, func() bool {
		assignments, ok := loadAssignments(nodes, slotCount)
		if !ok || len(assignments) != slotCount {
			return false
		}

		var probe *testNode
		for _, node := range nodes {
			if node != nil && node.cluster != nil {
				probe = node
				break
			}
		}
		if probe == nil {
			return false
		}

		for _, assignment := range assignments {
			slotNodes := make([]*testNode, 0, len(assignment.DesiredPeers))
			for _, peer := range assignment.DesiredPeers {
				idx := int(peer) - 1
				if idx < 0 || idx >= len(nodes) || nodes[idx] == nil || nodes[idx].cluster == nil {
					return false
				}
				slotNodes = append(slotNodes, nodes[idx])
			}
			if _, err := stableLeaderWithin(slotNodes, uint64(assignment.SlotID), testManagedSlotProbeWait); err != nil {
				return false
			}
			if _, err := probe.cluster.GetReconcileTask(context.Background(), assignment.SlotID); err == nil {
				return false
			} else if !errors.Is(err, controllermeta.ErrNotFound) {
				return false
			}
		}
		return true
	}, 30*time.Second, 100*time.Millisecond)
}

func waitForStableLeader(t testing.TB, testNodes []*testNode, slotID uint64) multiraft.NodeID {
	t.Helper()
	leaderID, err := stableLeaderWithin(testNodes, slotID, 20*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return leaderID
}

func waitForAllStableLeaders(t testing.TB, testNodes []*testNode, slotCount int) map[uint64]multiraft.NodeID {
	t.Helper()
	type result struct {
		slotID   uint64
		leaderID multiraft.NodeID
	}
	results := make(chan result, slotCount)
	for g := 1; g <= slotCount; g++ {
		go func(gid uint64) {
			lid := waitForStableLeader(t, testNodes, gid)
			results <- result{gid, lid}
		}(uint64(g))
	}
	leaders := make(map[uint64]multiraft.NodeID, slotCount)
	for range slotCount {
		r := <-results
		leaders[r.slotID] = r.leaderID
	}
	return leaders
}

func restartNode(t testing.TB, nodes []*testNode, idx int) *testNode {
	t.Helper()

	old := nodes[idx]
	if old == nil {
		t.Fatalf("nodes[%d] is nil", idx)
	}

	nodeID := old.nodeID
	dir := old.dir
	listenAddr := old.listenAddr
	clusterNodes := append([]raftcluster.NodeConfig(nil), old.nodes...)
	slots := append([]raftcluster.SlotConfig(nil), old.slots...)
	slotCount := old.slotCount

	old.stop()

	restarted := newStartedTestNodeWithHashSlots(
		t,
		dir,
		nodeID,
		listenAddr,
		clusterNodes,
		slots,
		slotCount,
		old.hashSlotCount,
		old.slotReplicaN,
		old.controllerReplicaN,
		old.withController,
	)
	nodes[idx] = restarted
	return restarted
}

func waitForChannelVisibleOnNodes(t testing.TB, nodes []*testNode, channelID string, channelType int64) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		allVisible := true
		for _, node := range nodes {
			if node == nil || node.store == nil {
				continue
			}
			ch, err := node.store.GetChannel(context.Background(), channelID, channelType)
			if err != nil || ch.ChannelID != channelID {
				allVisible = false
				break
			}
		}
		if allVisible {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("channel %q type=%d not visible on all running nodes", channelID, channelType)
}

func stableLeaderWithin(testNodes []*testNode, slotID uint64, timeout time.Duration) (multiraft.NodeID, error) {
	deadline := time.Now().Add(timeout)
	var stableLeader multiraft.NodeID
	stableCount := 0

	for time.Now().Before(deadline) {
		var leaderID multiraft.NodeID
		allAgree := true
		for _, n := range testNodes {
			if n == nil || n.cluster == nil {
				continue
			}
			lid, err := n.cluster.LeaderOf(multiraft.SlotID(slotID))
			if err != nil {
				allAgree = false
				break
			}
			if leaderID == 0 {
				leaderID = lid
			} else if lid != leaderID {
				allAgree = false
				break
			}
		}
		if allAgree && leaderID != 0 && activeNodePresent(testNodes, leaderID) {
			if leaderID == stableLeader {
				stableCount++
			} else {
				stableLeader = leaderID
				stableCount = 1
			}
			if stableCount >= testLeaderConfirmations {
				return stableLeader, nil
			}
		} else {
			stableCount = 0
			stableLeader = 0
		}
		time.Sleep(testLeaderPollInterval)
	}
	return 0, fmt.Errorf("no stable leader for slot %d", slotID)
}

func activeNodePresent(testNodes []*testNode, nodeID multiraft.NodeID) bool {
	for _, node := range testNodes {
		if node == nil || node.cluster == nil {
			continue
		}
		if node.nodeID == nodeID {
			return true
		}
	}
	return false
}

func snapshotAssignments(t testing.TB, nodes []*testNode, slotCount int) []controllermeta.SlotAssignment {
	t.Helper()

	var snapshot []controllermeta.SlotAssignment
	require.Eventually(t, func() bool {
		assignments, ok := loadAssignments(nodes, slotCount)
		if ok {
			snapshot = assignments
			return true
		}
		return false
	}, 10*time.Second, 100*time.Millisecond)
	return snapshot
}

func loadAssignments(nodes []*testNode, slotCount int) ([]controllermeta.SlotAssignment, bool) {
	for _, node := range nodes {
		if node == nil || node.cluster == nil {
			continue
		}
		assignments, err := node.cluster.ListSlotAssignments(context.Background())
		if err == nil && len(assignments) == slotCount {
			return assignments, true
		}
	}
	return nil, false
}

func assignmentsContainPeer(assignments []controllermeta.SlotAssignment, peer uint64) bool {
	for _, assignment := range assignments {
		for _, candidate := range assignment.DesiredPeers {
			if candidate == peer {
				return true
			}
		}
	}
	return false
}

func slotAssignedToPeerAndController(assignments []controllermeta.SlotAssignment, peer, controllerLeader uint64) uint32 {
	for _, assignment := range assignments {
		hasPeer := false
		hasControllerLeader := false
		for _, candidate := range assignment.DesiredPeers {
			if candidate == peer {
				hasPeer = true
			}
			if candidate == controllerLeader {
				hasControllerLeader = true
			}
		}
		if hasPeer && hasControllerLeader {
			return assignment.SlotID
		}
	}
	return 0
}

func slotForControllerLeader(assignments []controllermeta.SlotAssignment, controllerLeader uint64) (uint32, uint64) {
	for _, assignment := range assignments {
		hasLeader := false
		var sourceNode uint64
		for _, candidate := range assignment.DesiredPeers {
			if candidate == controllerLeader {
				hasLeader = true
				continue
			}
			if sourceNode == 0 || candidate < sourceNode {
				sourceNode = candidate
			}
		}
		if hasLeader && sourceNode != 0 {
			return assignment.SlotID, sourceNode
		}
	}
	return 0, 0
}

const (
	controllerProbeCodecVersion      byte  = 1
	controllerProbeKindAssignments   byte  = 3
	controllerProbeFlagNotLeader     byte  = 1 << 0
	controllerProbeServiceID         uint8 = 14
	controllerProbePayloadHeaderSize       = 10
)

func waitForControllerLeader(t testing.TB, nodes []*testNode) uint64 {
	t.Helper()

	var leaderID uint64
	require.Eventually(t, func() bool {
		controller, ok := currentControllerLeaderNode(nodes)
		if !ok {
			return false
		}
		leaderID = uint64(controller.nodeID)
		return true
	}, 10*time.Second, 100*time.Millisecond)
	return leaderID
}

func TestProbeControllerLeaderReturnsQuicklyWhenPeerIsStopped(t *testing.T) {
	nodes := startThreeNodesWithController(t, 1, 3)
	defer stopNodes(nodes)

	waitForControllerLeader(t, nodes)
	nodes[1].stop()

	start := time.Now()
	leaderID, ok := probeControllerLeader(nodes[0].cluster, 2)

	require.False(t, ok)
	require.Zero(t, leaderID)
	require.Less(t, time.Since(start), 500*time.Millisecond)
}

func TestProbeControllerLeaderDoesNotBlockOnUnresponsivePeer(t *testing.T) {
	hangLn, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer hangLn.Close()

	blockConn := make(chan struct{})
	defer close(blockConn)
	go func() {
		conn, err := hangLn.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		<-blockConn
	}()

	nodeLn, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	nodeAddr := nodeLn.Addr().String()
	require.NoError(t, nodeLn.Close())

	nodes := []raftcluster.NodeConfig{
		{NodeID: 1, Addr: nodeAddr},
		{NodeID: 2, Addr: hangLn.Addr().String()},
	}

	node := newStartedTestNode(
		t,
		filepath.Join(t.TempDir(), "n1"),
		1,
		nodeAddr,
		nodes,
		nil,
		1,
		1,
		1,
		true,
	)
	defer stopNodes([]*testNode{node})

	done := make(chan struct{})
	defer close(done)
	result := make(chan struct {
		leaderID multiraft.NodeID
		ok       bool
	}, 1)
	go func() {
		leaderID, ok := probeControllerLeader(node.cluster, 2)
		select {
		case result <- struct {
			leaderID multiraft.NodeID
			ok       bool
		}{leaderID: leaderID, ok: ok}:
		case <-done:
		}
	}()

	select {
	case got := <-result:
		require.Zero(t, got.leaderID)
		require.False(t, got.ok)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("probeControllerLeader blocked on unresponsive peer")
	}
}

func currentControllerLeaderNode(nodes []*testNode) (*testNode, bool) {
	for _, node := range nodes {
		if node == nil || node.cluster == nil {
			continue
		}
		controllerPeers := append([]raftcluster.NodeConfig(nil), node.nodes...)
		sort.Slice(controllerPeers, func(i, j int) bool {
			return controllerPeers[i].NodeID < controllerPeers[j].NodeID
		})
		if len(controllerPeers) > node.controllerReplicaN {
			controllerPeers = controllerPeers[:node.controllerReplicaN]
		}
		for _, peer := range controllerPeers {
			leaderID, ok := probeControllerLeader(node.cluster, peer.NodeID)
			if !ok {
				continue
			}
			idx := int(leaderID) - 1
			if idx >= 0 && idx < len(nodes) && nodes[idx] != nil && nodes[idx].cluster != nil {
				return nodes[idx], true
			}
		}
	}
	return nil, false
}

func probeControllerLeader(cluster *raftcluster.Cluster, peerID multiraft.NodeID) (multiraft.NodeID, bool) {
	if cluster == nil {
		return 0, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), testControllerProbeTimeout)
	defer cancel()
	respBody, err := cluster.RPCService(
		ctx,
		peerID,
		multiraft.SlotID(^uint32(0)),
		controllerProbeServiceID,
		[]byte{controllerProbeCodecVersion, controllerProbeKindAssignments, 0, 0, 0, 0, 0},
	)
	if err != nil {
		return 0, false
	}
	if len(respBody) < controllerProbePayloadHeaderSize+1 || respBody[0] != controllerProbeCodecVersion {
		return 0, false
	}
	payloadLen, n := binary.Uvarint(respBody[controllerProbePayloadHeaderSize:])
	if n <= 0 || len(respBody) != controllerProbePayloadHeaderSize+n+int(payloadLen) {
		return 0, false
	}
	if respBody[1]&controllerProbeFlagNotLeader != 0 {
		leaderID := multiraft.NodeID(binary.BigEndian.Uint64(respBody[2:10]))
		return leaderID, leaderID != 0
	}
	return peerID, true
}

func requireControllerCommand(t testing.TB, nodes []*testNode, fn func(*raftcluster.Cluster) error) {
	t.Helper()

	var lastErr error
	require.Eventually(t, func() bool {
		controller, ok := currentControllerLeaderNode(nodes)
		if !ok {
			lastErr = raftcluster.ErrNoLeader
			return false
		}
		lastErr = fn(controller.cluster)
		return lastErr == nil
	}, 15*time.Second, 200*time.Millisecond, "last controller command error: %v", lastErr)
}

func assignedNodesForSlot(t testing.TB, nodes []*testNode, slotID uint32) []*testNode {
	t.Helper()

	for _, node := range nodes {
		if node == nil || node.cluster == nil {
			continue
		}
		assignments, err := node.cluster.ListSlotAssignments(context.Background())
		if err != nil {
			continue
		}
		for _, assignment := range assignments {
			if assignment.SlotID != slotID {
				continue
			}
			assigned := make([]*testNode, 0, len(assignment.DesiredPeers))
			for _, peer := range assignment.DesiredPeers {
				idx := int(peer) - 1
				if idx >= 0 && idx < len(nodes) && nodes[idx] != nil {
					assigned = append(assigned, nodes[idx])
				}
			}
			require.NotEmpty(t, assigned)
			return assigned
		}
	}

	t.Fatalf("no assignment found for slot %d", slotID)
	return nil
}

func waitForLeader(t testing.TB, c *raftcluster.Cluster, slotID uint64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, err := c.LeaderOf(multiraft.SlotID(slotID))
		if err == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("no leader elected for slot %d", slotID)
}
