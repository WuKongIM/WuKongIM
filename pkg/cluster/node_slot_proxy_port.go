package cluster

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

type slotProxyRPCHandlerFunc func(context.Context, []byte) ([]byte, error)

func (f slotProxyRPCHandlerFunc) HandleRPC(ctx context.Context, payload []byte) ([]byte, error) {
	return f(ctx, payload)
}

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

// ProposeWithHashSlotResult submits an explicit Slot command and returns its FSM result.
func (n *Node) ProposeWithHashSlotResult(ctx context.Context, slotID multiraft.SlotID, hashSlot uint16, cmd []byte) ([]byte, error) {
	return n.ProposeResult(ctx, ProposeRequest{
		Command: cmd,
		Target: ProposeTarget{
			HashSlot:    hashSlot,
			HasHashSlot: true,
			SlotID:      uint32(slotID),
			HasSlotID:   true,
		},
	})
}

// RegisterSlotProxyRPC registers one function-style Slot proxy handler.
func (n *Node) RegisterSlotProxyRPC(serviceID uint8, handler func(context.Context, []byte) ([]byte, error)) {
	if handler == nil {
		return
	}
	n.RegisterRPC(serviceID, slotProxyRPCHandlerFunc(handler))
}

// GetChannelMetadataAuthoritative reads channel metadata from the current Slot leader.
func (n *Node) GetChannelMetadataAuthoritative(ctx context.Context, channelID string, channelType int64) (metadb.Channel, error) {
	if n == nil || n.defaultSlotProxy == nil {
		return metadb.Channel{}, ErrNotStarted
	}
	return n.defaultSlotProxy.GetChannelForPermission(ctx, channelID, channelType)
}

// CreateChannelMetadataStrict applies a create-only mutation at the Slot leader.
func (n *Node) CreateChannelMetadataStrict(ctx context.Context, channel metadb.Channel) error {
	if n == nil || n.defaultSlotProxy == nil {
		return ErrNotStarted
	}
	return n.defaultSlotProxy.CreateChannelMetadata(ctx, channel)
}

// PatchChannelBusinessFlags applies an existing-only partial flag mutation at the Slot leader.
func (n *Node) PatchChannelBusinessFlags(ctx context.Context, channelID string, channelType int64, flags metadb.ChannelBusinessFlags) error {
	if n == nil || n.defaultSlotProxy == nil {
		return ErrNotStarted
	}
	return n.defaultSlotProxy.PatchChannelBusinessFlags(ctx, channelID, channelType, flags)
}

// ListChannelSubscribersAuthoritative reads one subscriber page from the current Slot leader.
func (n *Node) ListChannelSubscribersAuthoritative(ctx context.Context, channelID string, channelType int64, afterUID string, limit int) ([]string, string, bool, error) {
	if n == nil || n.defaultSlotProxy == nil {
		return nil, "", false, ErrNotStarted
	}
	return n.defaultSlotProxy.ListChannelSubscribers(ctx, channelID, channelType, afterUID, limit)
}

// ContainsChannelSubscriberAuthoritative performs a Slot-leader point lookup.
func (n *Node) ContainsChannelSubscriberAuthoritative(ctx context.Context, channelID string, channelType int64, uid string) (bool, error) {
	if n == nil || n.defaultSlotProxy == nil {
		return false, ErrNotStarted
	}
	return n.defaultSlotProxy.ContainsChannelSubscriber(ctx, channelID, channelType, uid)
}

// HasChannelSubscribersAuthoritative checks set non-emptiness on the Slot leader.
func (n *Node) HasChannelSubscribersAuthoritative(ctx context.Context, channelID string, channelType int64) (bool, error) {
	if n == nil || n.defaultSlotProxy == nil {
		return false, ErrNotStarted
	}
	return n.defaultSlotProxy.HasChannelSubscribers(ctx, channelID, channelType)
}

// AddChannelSubscribersCounted applies a set add and returns its durable change count.
func (n *Node) AddChannelSubscribersCounted(ctx context.Context, channelID string, channelType int64, uids []string, mutationVersion uint64) (metadb.SubscriberMutationResult, error) {
	if n == nil || n.defaultSlotProxy == nil {
		return metadb.SubscriberMutationResult{}, ErrNotStarted
	}
	return n.defaultSlotProxy.AddChannelSubscribersCounted(ctx, channelID, channelType, uids, mutationVersion)
}

// RemoveChannelSubscribersCounted applies a set removal and returns its durable change count.
func (n *Node) RemoveChannelSubscribersCounted(ctx context.Context, channelID string, channelType int64, uids []string, mutationVersion uint64) (metadb.SubscriberMutationResult, error) {
	if n == nil || n.defaultSlotProxy == nil {
		return metadb.SubscriberMutationResult{}, ErrNotStarted
	}
	return n.defaultSlotProxy.RemoveChannelSubscribersCounted(ctx, channelID, channelType, uids, mutationVersion)
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
