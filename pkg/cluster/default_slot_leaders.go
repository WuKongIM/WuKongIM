package cluster

import (
	"context"
	"sort"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/slots"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
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
	n.mu.RLock()
	revision := n.controlSnapshot.Revision
	slotIDs, localAssignedSlotIDs := defaultSlotReadinessInputs(n.controlSnapshot.Slots, n.cfg.NodeID)
	n.mu.RUnlock()
	statuses := defaultSlotStatuses(n.defaultSlotRuntime, slotIDs)
	n.updateDefaultSlotsReady(revision, localAssignedSlotsReady(localAssignedSlotIDs, statuses))
	if n.cfg.seedJoinMode() && n.refreshSeedJoinRemoteSlotLeaders(context.Background()) {
		return
	}
	if len(slotIDs) == 0 {
		return
	}
	before := n.router.Table()
	n.router.UpdateSlotLeaders(routingSlotStatuses(statuses))
	n.publishRouteAuthorityChanges(before, n.router.Table())
}

// defaultSlotReadinessInputs copies only the physical Slot IDs needed by the 10ms readiness loop.
func defaultSlotReadinessInputs(assignments []control.SlotAssignment, localNodeID uint64) ([]uint32, []uint32) {
	slotIDs := make([]uint32, 0, len(assignments))
	localAssignedSlotIDs := make([]uint32, 0, len(assignments))
	for _, assignment := range assignments {
		if assignment.SlotID == 0 {
			continue
		}
		slotIDs = append(slotIDs, assignment.SlotID)
		for _, peerID := range assignment.DesiredPeers {
			if peerID == localNodeID {
				localAssignedSlotIDs = append(localAssignedSlotIDs, assignment.SlotID)
				break
			}
		}
	}
	return slotIDs, localAssignedSlotIDs
}

// defaultSlotStatuses returns the exact physical Slots whose local status read succeeded.
func defaultSlotStatuses(reader slots.StatusReader, slotIDs []uint32) []slots.Status {
	statuses := make([]slots.Status, 0, len(slotIDs))
	if reader == nil {
		return statuses
	}
	for _, slotID := range slotIDs {
		status, err := reader.Status(multiraft.SlotID(slotID))
		if err != nil || uint32(status.SlotID) != slotID {
			continue
		}
		statuses = append(statuses, slots.Status{
			SlotID: uint32(status.SlotID),
			Leader: uint64(status.LeaderID),
			Term:   status.Term,
		})
	}
	return statuses
}

// localAssignedSlotsReady requires a successful runtime status for every locally assigned physical Slot.
func localAssignedSlotsReady(localAssignedSlotIDs []uint32, statuses []slots.Status) bool {
	for _, slotID := range localAssignedSlotIDs {
		found := false
		for _, status := range statuses {
			if status.SlotID == slotID {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// updateDefaultSlotsReady publishes a readiness transition only for the current control revision.
func (n *Node) updateDefaultSlotsReady(revision uint64, ready bool) {
	if n == nil {
		return
	}
	n.mu.RLock()
	skip := n.controlSnapshot.Revision != revision || n.snapshot.StateRevision != revision || n.snapshot.SlotsReady == ready
	n.mu.RUnlock()
	if skip {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.controlSnapshot.Revision != revision || n.snapshot.StateRevision != revision {
		return
	}
	n.snapshot.SlotsReady = ready
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
	if n == nil || !n.cfg.seedJoinMode() {
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
