package cluster

import (
	"context"
	"sort"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/slots"
	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

const (
	defaultSeedJoinSlotLeaderPollInterval = 250 * time.Millisecond
	defaultRemoteSlotLeaderPollInterval   = time.Second
	remoteSlotLeaderRoundTimeout          = 250 * time.Millisecond
	remoteSlotLeaderMaxConcurrency        = 8
)

// startSlotLeaderLoop publishes local and remote default Slot leadership from
// independent managed loops so network observation cannot delay the 10ms
// local Raft readiness path.
func (n *Node) startSlotLeaderLoop() {
	if n == nil || n.defaultSlotRuntime == nil || n.slotLeaderCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	n.slotLeaderCancel = cancel
	n.slotLeaderWG.Add(2)
	goruntimeregistry.SafeGo(n.cfg.Goroutines, goruntimeregistry.TaskClusterSlotLeaderRefresh, func() {
		defer n.slotLeaderWG.Done()
		localTicker := time.NewTicker(n.slotLeaderPollInterval())
		defer localTicker.Stop()
		n.refreshDefaultSlotLeaders()
		for {
			select {
			case <-ctx.Done():
				return
			case <-localTicker.C:
				n.refreshDefaultSlotLeaders()
			}
		}
	})
	goruntimeregistry.SafeGo(n.cfg.Goroutines, goruntimeregistry.TaskClusterSlotLeaderRefresh, func() {
		defer n.slotLeaderWG.Done()
		remoteTicker := time.NewTicker(n.remoteSlotLeaderPollInterval())
		defer remoteTicker.Stop()
		n.refreshRemoteSlotLeaders(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-remoteTicker.C:
				n.refreshRemoteSlotLeaders(ctx)
			}
		}
	})
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
	if len(slotIDs) == 0 {
		return
	}
	_ = n.updateRouteAuthorityTable(func() error {
		n.router.UpdateSlotLeaders(routingSlotStatuses(statuses))
		return nil
	})
}

// defaultSlotReadinessInputs copies only the logical Slot Raft Group IDs needed by the 10ms readiness loop.
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

// defaultSlotStatuses returns the exact logical Slot Raft Groups whose local status read succeeded.
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

// localAssignedSlotsReady requires a successful runtime status for every locally assigned logical Slot Raft Group.
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

func (n *Node) remoteSlotLeaderPollInterval() time.Duration {
	if n != nil && n.cfg.seedJoinMode() {
		return defaultSeedJoinSlotLeaderPollInterval
	}
	return defaultRemoteSlotLeaderPollInterval
}

// refreshRemoteSlotLeaders copies one Controller snapshot and publishes only
// actual leaders observed from remote replicas within a bounded network round.
func (n *Node) refreshRemoteSlotLeaders(ctx context.Context) bool {
	if n == nil {
		return false
	}
	n.mu.RLock()
	snapshot := n.controlSnapshot.Clone()
	n.mu.RUnlock()
	if !n.installObservedRemoteSlotLeaders(ctx, snapshot) {
		return false
	}
	return true
}

// installObservedRemoteSlotLeaders updates non-local logical Slots without
// treating Controller preferred placement as current Raft authority.
func (n *Node) installObservedRemoteSlotLeaders(ctx context.Context, snapshot control.Snapshot) bool {
	if n == nil || n.router == nil || n.slotStatusCaller == nil {
		return false
	}
	if n.cfg.seedJoinMode() && !snapshotHasActiveNode(snapshot, n.cfg.NodeID) {
		return false
	}
	slotIDs := remoteSlotIDsFromSnapshot(snapshot, n.cfg.NodeID)
	if len(slotIDs) == 0 {
		return false
	}
	statuses := n.remoteSlotLeaderStatuses(ctx, snapshot, slotIDs)
	if len(statuses) == 0 {
		return false
	}
	_ = n.updateRouteAuthorityTable(func() error {
		n.router.UpdateSlotLeaders(statuses)
		return nil
	})
	return true
}

// remoteSlotLeaderStatuses queries at most eight peers concurrently and caps
// the entire observation round, independent of cluster node count.
func (n *Node) remoteSlotLeaderStatuses(ctx context.Context, snapshot control.Snapshot, slotIDs []uint32) []routing.SlotStatus {
	client := slotStatusClient{caller: n.slotStatusCaller}
	peerIDs := slotStatusPeerIDs(snapshot, n.cfg.NodeID, slotIDs)
	if len(peerIDs) == 0 {
		return nil
	}
	roundCtx, cancel := context.WithTimeout(ctx, remoteSlotLeaderRoundTimeout)
	defer cancel()
	type peerResult struct {
		statuses []routing.SlotStatus
	}
	jobs := make(chan uint64, len(peerIDs))
	results := make(chan peerResult, len(peerIDs))
	workerCount := remoteSlotLeaderWorkerCount(len(peerIDs))
	for range workerCount {
		goruntimeregistry.SafeGo(n.cfg.Goroutines, goruntimeregistry.TaskClusterSlotLeaderRefresh, func() {
			for {
				select {
				case <-roundCtx.Done():
					return
				case nodeID, ok := <-jobs:
					if !ok {
						return
					}
					statuses, err := client.Statuses(roundCtx, nodeID, slotIDs)
					if err != nil {
						results <- peerResult{}
						continue
					}
					results <- peerResult{statuses: statuses}
				}
			}
		})
	}
	for _, peerID := range peerIDs {
		jobs <- peerID
	}
	close(jobs)

	bySlot := make(map[uint32]routing.SlotStatus, len(slotIDs))
	for completed := 0; completed < len(peerIDs); completed++ {
		select {
		case <-roundCtx.Done():
			completed = len(peerIDs)
			continue
		case result := <-results:
			for _, status := range result.statuses {
				if status.SlotID == 0 || status.Leader == 0 {
					continue
				}
				current, ok := bySlot[status.SlotID]
				if !ok || status.LeaderTerm > current.LeaderTerm {
					bySlot[status.SlotID] = status
				}
			}
			if len(bySlot) == len(slotIDs) {
				cancel()
				completed = len(peerIDs)
			}
		}
	}
	out := make([]routing.SlotStatus, 0, len(bySlot))
	for _, status := range bySlot {
		out = append(out, status)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SlotID < out[j].SlotID })
	return out
}

func remoteSlotLeaderWorkerCount(peerCount int) int {
	return min(peerCount, remoteSlotLeaderMaxConcurrency)
}

func remoteSlotIDsFromSnapshot(snapshot control.Snapshot, localNodeID uint64) []uint32 {
	out := make([]uint32, 0, len(snapshot.Slots))
	for _, slot := range snapshot.Slots {
		if slot.SlotID == 0 || containsNodeID(slot.DesiredPeers, localNodeID) {
			continue
		}
		out = append(out, slot.SlotID)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func slotStatusPeerIDs(snapshot control.Snapshot, localNodeID uint64, slotIDs []uint32) []uint64 {
	wanted := make(map[uint32]struct{}, len(slotIDs))
	for _, slotID := range slotIDs {
		wanted[slotID] = struct{}{}
	}
	seen := make(map[uint64]struct{})
	for _, slot := range snapshot.Slots {
		if _, ok := wanted[slot.SlotID]; !ok {
			continue
		}
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

func containsNodeID(nodeIDs []uint64, want uint64) bool {
	for _, nodeID := range nodeIDs {
		if nodeID == want {
			return true
		}
	}
	return false
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
