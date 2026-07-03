package cluster

import (
	"context"
	"sort"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/slots"
)

const defaultSeedJoinSlotLeaderPollInterval = 250 * time.Millisecond

// startSlotLeaderLoop publishes local default Slot leadership into the foreground router.
func (n *Node) startSlotLeaderLoop() {
	if n == nil || n.defaultSlotRuntime == nil || n.slotLeaderCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	n.slotLeaderCancel = cancel
	n.slotLeaderWG.Add(1)
	go func() {
		defer n.slotLeaderWG.Done()
		ticker := time.NewTicker(n.slotLeaderPollInterval())
		defer ticker.Stop()
		for {
			n.refreshDefaultSlotLeaders()
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// stopSlotLeaderLoop stops the default Slot leadership publisher.
func (n *Node) stopSlotLeaderLoop() {
	if n == nil || n.slotLeaderCancel == nil {
		return
	}
	n.slotLeaderCancel()
	n.slotLeaderWG.Wait()
	n.slotLeaderCancel = nil
}

// refreshDefaultSlotLeaders maps local Multi-Raft status into routing slot leaders.
func (n *Node) refreshDefaultSlotLeaders() {
	if n == nil || n.defaultSlotRuntime == nil || n.router == nil {
		return
	}
	if n.refreshSeedJoinRemoteSlotLeaders(context.Background()) {
		return
	}
	slotIDs := n.currentSlotIDs()
	if len(slotIDs) == 0 {
		return
	}
	before := n.router.Table()
	n.router.UpdateSlotLeaders(routingSlotStatuses(slots.StatusSnapshot(n.defaultSlotRuntime, slotIDs)))
	n.publishRouteAuthorityChanges(before, n.router.Table())
}

func routingSlotStatuses(statuses []slots.Status) []routing.SlotStatus {
	out := make([]routing.SlotStatus, 0, len(statuses))
	for _, status := range statuses {
		out = append(out, routing.SlotStatus{SlotID: status.SlotID, Leader: status.Leader, LeaderTerm: status.Term})
	}
	return out
}

func (n *Node) slotLeaderPollInterval() time.Duration {
	if n != nil && n.cfg.seedJoinMode() {
		return defaultSeedJoinSlotLeaderPollInterval
	}
	return defaultSlotLeaderPollInterval
}

func (n *Node) refreshSeedJoinRemoteSlotLeaders(ctx context.Context) bool {
	if n == nil {
		return false
	}
	n.mu.RLock()
	snapshot := n.controlSnapshot.Clone()
	n.mu.RUnlock()
	before := n.router.Table()
	if !n.installSeedJoinActiveRemoteSlotLeaders(ctx, snapshot) {
		return false
	}
	n.publishRouteAuthorityChanges(before, n.router.Table())
	return true
}

func (n *Node) installSeedJoinActiveRemoteSlotLeaders(ctx context.Context, snapshot control.Snapshot) bool {
	if n == nil || n.router == nil || n.slotStatusCaller == nil || !n.cfg.seedJoinMode() || !snapshotHasActiveNode(snapshot, n.cfg.NodeID) {
		return false
	}
	slotIDs := slotIDsFromSnapshot(snapshot)
	if len(slotIDs) == 0 {
		return false
	}
	statuses := n.remoteSlotLeaderStatuses(ctx, snapshot, slotIDs)
	if len(statuses) == 0 {
		return false
	}
	n.router.UpdateSlotLeaders(statuses)
	return true
}

func (n *Node) remoteSlotLeaderStatuses(ctx context.Context, snapshot control.Snapshot, slotIDs []uint32) []routing.SlotStatus {
	client := slotStatusClient{caller: n.slotStatusCaller}
	bySlot := make(map[uint32]routing.SlotStatus, len(slotIDs))
	for _, nodeID := range slotStatusPeerIDs(snapshot, n.cfg.NodeID) {
		callCtx, cancel := context.WithTimeout(ctx, defaultSeedJoinSlotLeaderPollInterval)
		statuses, err := client.Statuses(callCtx, nodeID, slotIDs)
		cancel()
		if err != nil {
			continue
		}
		for _, status := range statuses {
			if status.SlotID == 0 || status.Leader == 0 {
				continue
			}
			bySlot[status.SlotID] = status
		}
		if len(bySlot) == len(slotIDs) {
			break
		}
	}
	out := make([]routing.SlotStatus, 0, len(bySlot))
	for _, status := range bySlot {
		out = append(out, status)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SlotID < out[j].SlotID })
	return out
}

func slotIDsFromSnapshot(snapshot control.Snapshot) []uint32 {
	out := make([]uint32, 0, len(snapshot.Slots))
	for _, slot := range snapshot.Slots {
		if slot.SlotID == 0 {
			continue
		}
		out = append(out, slot.SlotID)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func slotStatusPeerIDs(snapshot control.Snapshot, localNodeID uint64) []uint64 {
	seen := make(map[uint64]struct{})
	for _, slot := range snapshot.Slots {
		for _, peerID := range slot.DesiredPeers {
			if peerID == 0 || peerID == localNodeID {
				continue
			}
			seen[peerID] = struct{}{}
		}
	}
	out := make([]uint64, 0, len(seen))
	for peerID := range seen {
		out = append(out, peerID)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func snapshotHasActiveNode(snapshot control.Snapshot, nodeID uint64) bool {
	if nodeID == 0 {
		return false
	}
	for _, node := range snapshot.Nodes {
		if node.NodeID != nodeID {
			continue
		}
		return controlNodeJoinState(node.JoinState) == control.NodeJoinStateActive
	}
	return false
}

// currentSlotIDs returns the physical Slots visible in the latest local control snapshot.
func (n *Node) currentSlotIDs() []uint32 {
	n.mu.RLock()
	defer n.mu.RUnlock()
	out := make([]uint32, 0, len(n.controlSnapshot.Slots))
	for _, slot := range n.controlSnapshot.Slots {
		out = append(out, slot.SlotID)
	}
	return out
}
