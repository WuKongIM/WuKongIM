package routing

import (
	"fmt"
	"hash/crc32"
	"sync/atomic"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
)

// Router owns the atomic foreground route table.
type Router struct {
	current atomic.Pointer[Table]
}

// NewRouter creates an empty Router.
func NewRouter() *Router { return &Router{} }

// HashSlotForKey maps key to a logical hash slot using CRC32.
func HashSlotForKey(key string, count uint16) uint16 {
	if count == 0 {
		return 0
	}
	return uint16(crc32.ChecksumIEEE([]byte(key)) % uint32(count))
}

// Table returns the current immutable route table pointer.
func (r *Router) Table() *Table {
	if r == nil {
		return nil
	}
	return r.current.Load()
}

// RouteKey routes key through the current table.
func (r *Router) RouteKey(key string) (Route, error) {
	table := r.Table()
	if table == nil {
		return Route{}, ErrRouteNotReady
	}
	return table.routeHashSlot(HashSlotForKey(key, table.HashSlotCount))
}

// RouteHashSlot routes hashSlot through the current table.
func (r *Router) RouteHashSlot(hashSlot uint16) (Route, error) {
	table := r.Table()
	if table == nil {
		return Route{}, ErrRouteNotReady
	}
	return table.routeHashSlot(hashSlot)
}

// RouteSlot validates and routes an explicit physical Slot/hash-slot pair.
func (r *Router) RouteSlot(slotID uint32, hashSlot uint16) (Route, error) {
	table := r.Table()
	if table == nil {
		return Route{}, ErrRouteNotReady
	}
	if int(hashSlot) >= len(table.HashToSlot) {
		return Route{}, fmt.Errorf("%w: hash slot %d out of range", ErrRouteNotReady, hashSlot)
	}
	if table.HashToSlot[int(hashSlot)] != slotID {
		return Route{}, ErrRouteMismatch
	}
	return table.routeHashSlot(hashSlot)
}

// UpdateControlSnapshot builds and installs a new route table from snapshot.
func (r *Router) UpdateControlSnapshot(snapshot control.Snapshot) error {
	table, err := BuildTable(snapshot)
	if err != nil {
		return err
	}
	if current := r.Table(); current != nil {
		for slotID, leader := range current.SlotLeaders {
			if _, ok := table.SlotPeers[slotID]; ok {
				table.SlotLeaders[slotID] = leader
			}
		}
	}
	r.current.Store(table)
	return nil
}

// UpdateSlotLeaders atomically installs observed Slot leaders on top of the current table.
func (r *Router) UpdateSlotLeaders(status []SlotStatus) {
	if r == nil {
		return
	}
	for {
		current := r.current.Load()
		if current == nil {
			return
		}
		next := current.cloneWithLeaders(status)
		if r.current.CompareAndSwap(current, next) {
			return
		}
	}
}
