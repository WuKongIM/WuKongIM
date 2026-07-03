package cluster

import (
	"sync/atomic"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

type Router struct {
	hashSlotTable atomic.Pointer[HashSlotTable]
	runtime       *multiraft.Runtime
	localNode     multiraft.NodeID
}

func NewRouter(table *HashSlotTable, localNode multiraft.NodeID, runtime *multiraft.Runtime) *Router {
	r := &Router{
		runtime:   runtime,
		localNode: localNode,
	}
	r.UpdateHashSlotTable(table)
	return r
}

func (r *Router) SlotForKey(key string) multiraft.SlotID {
	table := r.hashSlotTable.Load()
	if table == nil {
		return 0
	}
	return table.Lookup(r.HashSlotForKey(key))
}

func (r *Router) HashSlotForKey(key string) uint16 {
	table := r.hashSlotTable.Load()
	if table == nil {
		return 0
	}
	return HashSlotForKey(key, table.HashSlotCount())
}

func (r *Router) UpdateHashSlotTable(table *HashSlotTable) {
	if table == nil {
		r.hashSlotTable.Store(nil)
		return
	}
	r.hashSlotTable.Store(table.Clone())
}

func (r *Router) HashSlotsOf(slotID multiraft.SlotID) []uint16 {
	table := r.hashSlotTable.Load()
	if table == nil {
		return nil
	}
	return table.HashSlotsOf(slotID)
}

func (r *Router) LeaderOf(slotID multiraft.SlotID) (multiraft.NodeID, error) {
	if r == nil || r.runtime == nil {
		return 0, ErrNotStarted
	}
	status, err := r.runtime.Status(slotID)
	if err != nil {
		return 0, err
	}
	if status.LeaderID == 0 {
		return 0, ErrNoLeader
	}
	return status.LeaderID, nil
}

func (r *Router) IsLocal(nodeID multiraft.NodeID) bool {
	return nodeID == r.localNode
}
