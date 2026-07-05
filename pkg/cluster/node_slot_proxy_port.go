package cluster

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

// SlotIDs returns the physical Slots visible in the local control snapshot.
func (n *Node) SlotIDs() []multiraft.SlotID {
	if n == nil {
		return nil
	}
	n.mu.RLock()
	defer n.mu.RUnlock()
	out := make([]multiraft.SlotID, 0, len(n.controlSnapshot.Slots))
	for _, slot := range n.controlSnapshot.Slots {
		if slot.SlotID == 0 {
			continue
		}
		out = append(out, multiraft.SlotID(slot.SlotID))
	}
	return out
}

// SlotForKey maps key to its current physical Slot.
func (n *Node) SlotForKey(key string) multiraft.SlotID {
	route, err := n.RouteKey(key)
	if err != nil {
		return 0
	}
	return multiraft.SlotID(route.SlotID)
}

// HashSlotForKey maps key to a logical hash slot using the installed table size.
func (n *Node) HashSlotForKey(key string) uint16 {
	if n == nil {
		return 0
	}
	count := uint16(0)
	if n.router != nil {
		if table := n.router.Table(); table != nil {
			count = table.HashSlotCount
		}
	}
	if count == 0 {
		n.mu.RLock()
		count = n.snapshot.HashSlotCount
		if count == 0 {
			count = n.controlSnapshot.HashSlots.Count
		}
		n.mu.RUnlock()
	}
	if count == 0 {
		count = n.cfg.Slots.HashSlotCount
	}
	return routing.HashSlotForKey(key, count)
}

// HashSlotsOf returns logical hash slots currently assigned to slotID.
func (n *Node) HashSlotsOf(slotID multiraft.SlotID) []uint16 {
	if n == nil {
		return nil
	}
	n.mu.RLock()
	table := n.controlSnapshot.HashSlots
	n.mu.RUnlock()
	return hashSlotsOfPhysicalSlot(table, uint32(slotID))
}

// HashSlotTableVersion returns the local control snapshot hash-slot table revision.
func (n *Node) HashSlotTableVersion() uint64 {
	if n == nil {
		return 0
	}
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.controlSnapshot.HashSlots.Revision
}

// LeaderOf returns the best-known Slot leader from the foreground router.
func (n *Node) LeaderOf(slotID multiraft.SlotID) (multiraft.NodeID, error) {
	if err := n.ensureForeground(); err != nil {
		return 0, err
	}
	if n.router == nil {
		return 0, ErrRouteNotReady
	}
	table := n.router.Table()
	if table == nil {
		return 0, ErrRouteNotReady
	}
	peers, ok := table.SlotPeers[uint32(slotID)]
	if !ok || len(peers) == 0 {
		return 0, ErrSlotNotFound
	}
	leader := table.SlotLeaders[uint32(slotID)]
	if leader == 0 {
		return 0, ErrNoSlotLeader
	}
	return multiraft.NodeID(leader), nil
}

// IsLocal reports whether nodeID is this cluster node.
func (n *Node) IsLocal(nodeID multiraft.NodeID) bool {
	return n != nil && uint64(nodeID) == n.cfg.NodeID
}

// PeersForSlot returns desired Slot replica peers from the foreground router.
func (n *Node) PeersForSlot(slotID multiraft.SlotID) []multiraft.NodeID {
	if n == nil || n.router == nil {
		return nil
	}
	table := n.router.Table()
	if table == nil {
		return nil
	}
	peers := table.SlotPeers[uint32(slotID)]
	out := make([]multiraft.NodeID, 0, len(peers))
	for _, peer := range peers {
		out = append(out, multiraft.NodeID(peer))
	}
	return out
}

// RPCService invokes a node-scoped RPC service; slotID is retained for legacy proxy compatibility.
func (n *Node) RPCService(ctx context.Context, nodeID multiraft.NodeID, _ multiraft.SlotID, serviceID uint8, payload []byte) ([]byte, error) {
	return n.CallRPC(ctx, uint64(nodeID), serviceID, payload)
}

// ProposeWithHashSlot submits a Slot metadata command to an explicit Slot/hash-slot target.
func (n *Node) ProposeWithHashSlot(ctx context.Context, slotID multiraft.SlotID, hashSlot uint16, cmd []byte) error {
	return n.Propose(ctx, ProposeRequest{
		Command: cmd,
		Target: ProposeTarget{
			HashSlot:    hashSlot,
			HasHashSlot: true,
			SlotID:      uint32(slotID),
			HasSlotID:   true,
		},
	})
}

// ProposeLocalWithHashSlot submits only when this node is the current Slot leader.
func (n *Node) ProposeLocalWithHashSlot(ctx context.Context, slotID multiraft.SlotID, hashSlot uint16, cmd []byte) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if err := n.ensureForeground(); err != nil {
		return err
	}
	if n.router == nil {
		return ErrRouteNotReady
	}
	route, err := n.router.RouteSlot(uint32(slotID), hashSlot)
	if err != nil {
		return mapRouteError(err)
	}
	if route.Leader != n.cfg.NodeID {
		return ErrNotLeader
	}
	return n.ProposeWithHashSlot(ctx, slotID, hashSlot, cmd)
}
