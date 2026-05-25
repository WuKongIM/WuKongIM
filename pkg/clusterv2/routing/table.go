package routing

import (
	"errors"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
)

var (
	// ErrRouteNotReady indicates that no route table is installed.
	ErrRouteNotReady = errors.New("clusterv2/routing: route not ready")
	// ErrNoSlotLeader indicates that the route exists but has no known leader.
	ErrNoSlotLeader = errors.New("clusterv2/routing: no slot leader")
	// ErrRouteMismatch indicates that an explicit slot/hash-slot pair disagrees with the route table.
	ErrRouteMismatch = errors.New("clusterv2/routing: route mismatch")
)

// Table is an immutable route table optimized for foreground lookups.
type Table struct {
	// Revision is the control snapshot revision that produced this table.
	Revision uint64
	// HashToSlot maps hash slot index to physical Slot ID.
	HashToSlot []uint32
	// SlotLeaders maps physical Slot ID to best-known leader node ID.
	SlotLeaders map[uint32]uint64
	// SlotPeers maps physical Slot ID to desired replica node IDs.
	SlotPeers map[uint32][]uint64
	// HashSlotCount is the number of logical hash slots covered by HashToSlot.
	HashSlotCount uint16
}

// Route describes the current routing decision for one hash slot.
type Route struct {
	// HashSlot is the logical hash slot selected for the request.
	HashSlot uint16
	// SlotID is the physical Slot that owns HashSlot.
	SlotID uint32
	// Leader is the best-known Slot Raft leader node ID.
	Leader uint64
	// Peers are the desired Slot replica node IDs.
	Peers []uint64
	// Revision is the control snapshot revision that produced this route.
	Revision uint64
}

// SlotStatus carries observed Slot leadership into the routing table.
type SlotStatus struct {
	// SlotID is the physical Slot ID.
	SlotID uint32
	// Leader is the best-known leader node ID for SlotID.
	Leader uint64
}

// BuildTable converts a control snapshot into an immutable route table.
func BuildTable(snapshot control.Snapshot) (*Table, error) {
	if err := snapshot.Validate(); err != nil {
		return nil, err
	}
	count := snapshot.HashSlots.Count
	table := &Table{
		Revision:      snapshot.Revision,
		HashToSlot:    make([]uint32, int(count)),
		SlotLeaders:   make(map[uint32]uint64, len(snapshot.Slots)),
		SlotPeers:     make(map[uint32][]uint64, len(snapshot.Slots)),
		HashSlotCount: count,
	}
	for _, slot := range snapshot.Slots {
		table.SlotPeers[slot.SlotID] = append([]uint64(nil), slot.DesiredPeers...)
	}
	for _, r := range snapshot.HashSlots.Ranges {
		for hashSlot := r.From; hashSlot <= r.To; hashSlot++ {
			table.HashToSlot[int(hashSlot)] = r.SlotID
			if hashSlot == r.To {
				break
			}
		}
	}
	return table, nil
}

func (t *Table) routeHashSlot(hashSlot uint16) (Route, error) {
	if t == nil || len(t.HashToSlot) == 0 {
		return Route{}, ErrRouteNotReady
	}
	if int(hashSlot) >= len(t.HashToSlot) {
		return Route{}, fmt.Errorf("%w: hash slot %d out of range", ErrRouteNotReady, hashSlot)
	}
	slotID := t.HashToSlot[int(hashSlot)]
	if slotID == 0 {
		return Route{}, ErrRouteNotReady
	}
	leader := t.SlotLeaders[slotID]
	if leader == 0 {
		return Route{}, ErrNoSlotLeader
	}
	return Route{HashSlot: hashSlot, SlotID: slotID, Leader: leader, Peers: append([]uint64(nil), t.SlotPeers[slotID]...), Revision: t.Revision}, nil
}

func (t *Table) cloneWithLeaders(status []SlotStatus) *Table {
	if t == nil {
		return nil
	}
	out := &Table{
		Revision:      t.Revision,
		HashToSlot:    append([]uint32(nil), t.HashToSlot...),
		SlotLeaders:   make(map[uint32]uint64, len(t.SlotLeaders)+len(status)),
		SlotPeers:     make(map[uint32][]uint64, len(t.SlotPeers)),
		HashSlotCount: t.HashSlotCount,
	}
	for slotID, leader := range t.SlotLeaders {
		out.SlotLeaders[slotID] = leader
	}
	for slotID, peers := range t.SlotPeers {
		out.SlotPeers[slotID] = append([]uint64(nil), peers...)
	}
	for _, item := range status {
		if item.SlotID == 0 {
			continue
		}
		if item.Leader == 0 {
			delete(out.SlotLeaders, item.SlotID)
			continue
		}
		out.SlotLeaders[item.SlotID] = item.Leader
	}
	return out
}
