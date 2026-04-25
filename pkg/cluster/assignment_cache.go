package cluster

import (
	"sync"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

type assignmentCache struct {
	mu           sync.RWMutex
	assignments  []controllermeta.SlotAssignment
	peersByGroup map[multiraft.SlotID][]multiraft.NodeID
}

func newAssignmentCache() *assignmentCache {
	return &assignmentCache{
		peersByGroup: make(map[multiraft.SlotID][]multiraft.NodeID),
	}
}

func (c *assignmentCache) SetAssignments(assignments []controllermeta.SlotAssignment) {
	if c == nil {
		return
	}

	peersByGroup := make(map[multiraft.SlotID][]multiraft.NodeID, len(assignments))
	cloned := make([]controllermeta.SlotAssignment, 0, len(assignments))
	for _, assignment := range assignments {
		cloned = append(cloned, controllermeta.SlotAssignment{
			SlotID:         assignment.SlotID,
			DesiredPeers:   append([]uint64(nil), assignment.DesiredPeers...),
			ConfigEpoch:    assignment.ConfigEpoch,
			BalanceVersion: assignment.BalanceVersion,
		})

		peers := make([]multiraft.NodeID, 0, len(assignment.DesiredPeers))
		for _, peer := range assignment.DesiredPeers {
			peers = append(peers, multiraft.NodeID(peer))
		}
		peersByGroup[multiraft.SlotID(assignment.SlotID)] = peers
	}

	c.mu.Lock()
	c.assignments = cloned
	c.peersByGroup = peersByGroup
	c.mu.Unlock()
}

func (c *assignmentCache) PeersForSlot(slotID multiraft.SlotID) ([]multiraft.NodeID, bool) {
	if c == nil {
		return nil, false
	}

	c.mu.RLock()
	peers, ok := c.peersByGroup[slotID]
	c.mu.RUnlock()
	if !ok {
		return nil, false
	}
	return append([]multiraft.NodeID(nil), peers...), true
}

func (c *assignmentCache) Snapshot() []controllermeta.SlotAssignment {
	if c == nil {
		return nil
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	out := make([]controllermeta.SlotAssignment, 0, len(c.assignments))
	for _, assignment := range c.assignments {
		out = append(out, controllermeta.SlotAssignment{
			SlotID:         assignment.SlotID,
			DesiredPeers:   append([]uint64(nil), assignment.DesiredPeers...),
			ConfigEpoch:    assignment.ConfigEpoch,
			BalanceVersion: assignment.BalanceVersion,
		})
	}
	return out
}

func (c *assignmentCache) ConfigEpochForSlot(slotID multiraft.SlotID) (uint64, bool) {
	if c == nil {
		return 0, false
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	for _, assignment := range c.assignments {
		if assignment.SlotID == uint32(slotID) {
			return assignment.ConfigEpoch, true
		}
	}
	return 0, false
}
