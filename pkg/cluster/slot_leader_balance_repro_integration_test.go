//go:build integration

package cluster

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	clustertasks "github.com/WuKongIM/WuKongIM/pkg/cluster/tasks"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

// TestSlotLeaderPlacementRecoversPreferredBalance reproduces the live failure
// where legal Raft transfers leave actual Slot leaders at 2/8/0 while the
// Controller's PreferredLeader placement remains balanced.
func TestSlotLeaderPlacementRecoversPreferredBalance(t *testing.T) {
	nodes, placementGates := newTenSlotThreeNodeCluster(t)
	startNodes(t, nodes...)
	t.Cleanup(func() { stopSlotLeaderReproNodes(t, nodes) })
	waitClusterReady(t, nodes...)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	snapshot := waitTenSlotRuntimeConverged(t, ctx, nodes)
	if len(snapshot.Slots) != 10 {
		t.Fatalf("physical Slots = %d, want 10", len(snapshot.Slots))
	}
	wantBalanced := preferredSlotLeaderCounts(snapshot.Slots)
	if !slotLeaderCountsBalanced(wantBalanced, 10) {
		t.Fatalf("preferred leaders = %v, want ten Slots spread within one", wantBalanced)
	}

	// Pin a known-good baseline first so the reproduction does not depend on
	// whichever legal voters won the bootstrap election.
	for _, assignment := range snapshot.Slots {
		transferSlotLeaderAndWait(t, nodes, assignment.SlotID, assignment.PreferredLeader)
	}
	if got := actualSlotLeaderCounts(t, nodes[0], snapshot.Slots); !reflect.DeepEqual(got, wantBalanced) {
		t.Fatalf("aligned actual leaders = %v, want preferred baseline %v", got, wantBalanced)
	}

	// Reproduce the observed legal-but-skewed runtime state: two leaders on
	// node 1, eight on node 2, and none on node 3. All three voters remain
	// healthy and caught up, so every transfer still obeys Raft rules.
	for i, assignment := range snapshot.Slots {
		target := uint64(2)
		if i < 2 {
			target = 1
		}
		transferSlotLeaderAndWait(t, nodes, assignment.SlotID, target)
	}
	wantSkewed := map[uint64]int{1: 2, 2: 8, 3: 0}
	if got := actualSlotLeaderCounts(t, nodes[0], snapshot.Slots); !reflect.DeepEqual(got, wantSkewed) {
		t.Fatalf("reproduction leaders = %v, want exact 2/8/0 skew %v", got, wantSkewed)
	}
	for _, gate := range placementGates {
		gate.Enable()
	}

	// Drive the production low-frequency preferred-leader seam directly so
	// this regression remains fast. Leader observation remains read-only; the
	// placement reconciler must return eligible actual leaders to the still
	// balanced PreferredLeader plan instead of accepting permanent 2/8/0 skew.
	// A target can be briefly behind immediately after the forced skew. This
	// test shortens only the dedupe cooldown so it can exercise the same strict
	// production path repeatedly without waiting for the five-second default.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for i, node := range nodes {
			if err := placementGates[i].Reconcile(ctx, snapshot); err != nil {
				t.Fatalf("preferredLeaderReconcile(node=%d) error = %v", node.NodeID(), err)
			}
			node.refreshDefaultSlotLeaders()
		}
		if got := actualSlotLeaderCounts(t, nodes[0], snapshot.Slots); reflect.DeepEqual(got, wantBalanced) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("actual Slot leaders remained %v after persistent skew; PreferredLeader plan is %v", actualSlotLeaderCounts(t, nodes[0], snapshot.Slots), wantBalanced)
}

func waitTenSlotRuntimeConverged(t *testing.T, ctx context.Context, nodes []*Node) control.Snapshot {
	t.Helper()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		snapshot, err := nodes[0].LocalControlSnapshot(ctx)
		if err == nil && len(snapshot.Slots) == 10 {
			ready := true
			for _, node := range nodes {
				for _, assignment := range snapshot.Slots {
					status, statusErr := node.defaultSlotRuntime.Status(multiraft.SlotID(assignment.SlotID))
					if statusErr != nil || status.LeaderID == 0 || len(status.CurrentVoters) != 3 {
						ready = false
						break
					}
				}
				if !ready {
					break
				}
			}
			if ready {
				return snapshot
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("ten-Slot runtime did not converge: %v; statuses=%s", ctx.Err(), slotRuntimeSummary(nodes))
		case <-ticker.C:
		}
	}
}

func slotRuntimeSummary(nodes []*Node) string {
	var out string
	for _, node := range nodes {
		out += fmt.Sprintf(" n%d=[", node.NodeID())
		for slotID := uint32(1); slotID <= 10; slotID++ {
			status, err := node.defaultSlotRuntime.Status(multiraft.SlotID(slotID))
			if err != nil {
				out += fmt.Sprintf("s%d:%v,", slotID, err)
				continue
			}
			out += fmt.Sprintf("s%d:l%d/v%d,", slotID, status.LeaderID, len(status.CurrentVoters))
		}
		out += "]"
	}
	return out
}

func newTenSlotThreeNodeCluster(t *testing.T) ([]*Node, []*slotLeaderPlacementGate) {
	t.Helper()
	addrs := []string{freeTCPAddr(t), freeTCPAddr(t), freeTCPAddr(t)}
	clusterID := fmt.Sprintf("slot-leader-balance-repro-%d", time.Now().UnixNano())
	snapshot := tenSlotControlSnapshot(clusterID, addrs)
	voters := []ControlVoter{{NodeID: 1, Addr: addrs[0]}, {NodeID: 2, Addr: addrs[1]}, {NodeID: 3, Addr: addrs[2]}}
	nodes := make([]*Node, 0, len(addrs))
	gates := make([]*slotLeaderPlacementGate, 0, len(addrs))
	for i, addr := range addrs {
		nodeID := uint64(i + 1)
		cfg := Config{NodeID: nodeID, ListenAddr: addr, DataDir: t.TempDir()}
		cfg.Control.ClusterID = clusterID
		cfg.Control.Voters = voters
		cfg.Slots.InitialSlotCount = 10
		cfg.Slots.HashSlotCount = 256
		cfg.Slots.ReplicaCount = 3
		// Keep the election budget wide enough for -race instrumentation; a
		// 20ms quorum window can trigger unrelated elections while the test is
		// deliberately transferring ten leaders in sequence.
		cfg.Slots.TickInterval = 2 * time.Millisecond
		cfg.Slots.ElectionTick = 100
		cfg.Slots.HeartbeatTick = 1
		cfg.Channel.TickInterval = time.Millisecond
		gate := &slotLeaderPlacementGate{}
		node, err := New(cfg, withTaskExecutor(&recordingTaskExecutor{}), withPreferredLeaderReconciler(gate), withPreferredLeaderReconcileInterval(10*time.Millisecond))
		if err != nil {
			t.Fatalf("New(node=%d) error = %v", nodeID, err)
		}
		gate.node = node
		if err := node.ensureDefaultTransport(); err != nil {
			t.Fatalf("ensureDefaultTransport(node=%d) error = %v", nodeID, err)
		}
		node.control = control.NewStaticController(snapshot)
		nodes = append(nodes, node)
		gates = append(gates, gate)
	}
	return nodes, gates
}

type slotLeaderPlacementGate struct {
	node    *Node
	enabled atomic.Bool
	once    sync.Once
	inner   *clustertasks.PreferredLeaderReconciler
}

func (g *slotLeaderPlacementGate) Enable() {
	g.enabled.Store(true)
}

func (g *slotLeaderPlacementGate) Reconcile(ctx context.Context, snapshot control.Snapshot) error {
	if g == nil || !g.enabled.Load() {
		return nil
	}
	g.once.Do(func() {
		g.inner = clustertasks.NewPreferredLeaderReconciler(clustertasks.PreferredLeaderReconcilerConfig{
			LocalNode:   g.node.NodeID(),
			Runtime:     g.node.defaultSlotRuntime,
			Cooldown:    25 * time.Millisecond,
			IntentGuard: g.node.preferredLeaderIntentGuard,
		})
	})
	return g.inner.Reconcile(ctx, snapshot)
}

func tenSlotControlSnapshot(clusterID string, addrs []string) control.Snapshot {
	preferred := []uint64{2, 1, 3, 2, 1, 3, 2, 1, 3, 2}
	ranges := []control.HashSlotRange{
		{From: 0, To: 25, SlotID: 1},
		{From: 26, To: 51, SlotID: 2},
		{From: 52, To: 77, SlotID: 3},
		{From: 78, To: 103, SlotID: 4},
		{From: 104, To: 129, SlotID: 5},
		{From: 130, To: 155, SlotID: 6},
		{From: 156, To: 180, SlotID: 7},
		{From: 181, To: 205, SlotID: 8},
		{From: 206, To: 230, SlotID: 9},
		{From: 231, To: 255, SlotID: 10},
	}
	snapshot := control.Snapshot{
		ClusterID:    clusterID,
		Revision:     1,
		ControllerID: 1,
		HashSlots:    control.HashSlotTable{Revision: 1, Count: 256, Ranges: ranges},
	}
	for i, addr := range addrs {
		snapshot.Nodes = append(snapshot.Nodes, control.Node{
			NodeID:    uint64(i + 1),
			Addr:      addr,
			Roles:     []control.Role{control.RoleData},
			Status:    control.NodeAlive,
			JoinState: control.NodeJoinStateActive,
		})
	}
	for i, target := range preferred {
		snapshot.Slots = append(snapshot.Slots, control.SlotAssignment{
			SlotID:          uint32(i + 1),
			DesiredPeers:    []uint64{1, 2, 3},
			ConfigEpoch:     1,
			PreferredLeader: target,
		})
	}
	return snapshot
}

func preferredSlotLeaderCounts(assignments []control.SlotAssignment) map[uint64]int {
	counts := map[uint64]int{1: 0, 2: 0, 3: 0}
	for _, assignment := range assignments {
		counts[assignment.PreferredLeader]++
	}
	return counts
}

func actualSlotLeaderCounts(t *testing.T, observer *Node, assignments []control.SlotAssignment) map[uint64]int {
	t.Helper()
	counts := map[uint64]int{1: 0, 2: 0, 3: 0}
	for _, assignment := range assignments {
		status, err := observer.defaultSlotRuntime.Status(multiraft.SlotID(assignment.SlotID))
		if err != nil {
			t.Fatalf("Status(slot=%d) error = %v", assignment.SlotID, err)
		}
		counts[uint64(status.LeaderID)]++
	}
	return counts
}

func slotLeaderCountsBalanced(counts map[uint64]int, total int) bool {
	if counts[1]+counts[2]+counts[3] != total {
		return false
	}
	minCount, maxCount := counts[1], counts[1]
	for _, nodeID := range []uint64{2, 3} {
		if counts[nodeID] < minCount {
			minCount = counts[nodeID]
		}
		if counts[nodeID] > maxCount {
			maxCount = counts[nodeID]
		}
	}
	return maxCount-minCount <= 1
}

func transferSlotLeaderAndWait(t *testing.T, nodes []*Node, slotID uint32, target uint64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var current uint64
		for _, node := range nodes {
			status, err := node.defaultSlotRuntime.Status(multiraft.SlotID(slotID))
			if err == nil && status.LeaderID != 0 {
				current = uint64(status.LeaderID)
				break
			}
		}
		if current == target {
			allObserved := true
			for _, node := range nodes {
				status, err := node.defaultSlotRuntime.Status(multiraft.SlotID(slotID))
				if err != nil || uint64(status.LeaderID) != target {
					allObserved = false
					break
				}
			}
			if allObserved {
				return
			}
			time.Sleep(10 * time.Millisecond)
			continue
		}
		if current != 0 {
			leader := nodes[current-1]
			if err := leader.defaultSlotRuntime.TransferLeadership(context.Background(), multiraft.SlotID(slotID), multiraft.NodeID(target)); err != nil {
				t.Fatalf("TransferLeadership(slot=%d,target=%d) error = %v", slotID, target, err)
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("slot %d did not transfer to node %d", slotID, target)
}

func stopSlotLeaderReproNodes(t *testing.T, nodes []*Node) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	errs := make(chan error, len(nodes))
	for _, node := range nodes {
		node := node
		go func() { errs <- node.Stop(ctx) }()
	}
	for range nodes {
		if err := <-errs; err != nil {
			t.Errorf("Stop() error = %v", err)
		}
	}
}
