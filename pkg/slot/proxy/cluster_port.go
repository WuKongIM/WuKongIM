package proxy

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

// Cluster is the Slot proxy's minimal routing, proposal, and RPC surface.
type Cluster interface {
	SlotIDs() []multiraft.SlotID
	SlotForKey(string) multiraft.SlotID
	HashSlotForKey(string) uint16
	HashSlotsOf(multiraft.SlotID) []uint16
	HashSlotTableVersion() uint64
	LeaderOf(multiraft.SlotID) (multiraft.NodeID, error)
	IsLocal(multiraft.NodeID) bool
	PeersForSlot(multiraft.SlotID) []multiraft.NodeID
	RPCService(context.Context, multiraft.NodeID, multiraft.SlotID, uint8, []byte) ([]byte, error)
}

func clusterNodeID(cluster any) uint64 {
	if cluster == nil {
		return 0
	}
	if node, ok := cluster.(interface{ NodeID() uint64 }); ok {
		return node.NodeID()
	}
	if node, ok := cluster.(interface{ NodeID() multiraft.NodeID }); ok {
		return uint64(node.NodeID())
	}
	return 0
}
