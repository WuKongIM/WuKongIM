package routing

import (
	"fmt"
	"hash/crc32"
	"sync/atomic"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
)

// Router owns the atomic foreground route table.
type Router struct {
	current atomic.Pointer[Table]
}

// RouteKeyResult is the aligned routing outcome for one batch key.
type RouteKeyResult struct {
	// Route is populated when Err is nil.
	Route Route
	// Err records a key-specific routing failure.
	Err error
}

// NewRouter creates an empty Router.
func NewRouter() *Router { return &Router{} }

// HashSlotForKey maps key to a physical hash slot using CRC32.
func HashSlotForKey(key string, count uint16) uint16 {
	if count == 0 {
		return 0
	}
	return uint16(checksumIEEEString(key) % uint32(count))
}

func checksumIEEEString(value string) uint32 {
	crc := ^uint32(0)
	for i := 0; i < len(value); i++ {
		crc = crc32.IEEETable[byte(crc)^value[i]] ^ (crc >> 8)
	}
	return ^crc
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
	route, hashSlot, err := routeKey(table, key)
	if err != nil {
		return Route{}, fmt.Errorf("route key=%q hashSlot=%d: %w", key, hashSlot, err)
	}
	return route, nil
}

// RouteKeys routes keys through one current table snapshot and preserves input order.
func (r *Router) RouteKeys(keys []string) ([]Route, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	table := r.Table()
	if table == nil {
		return nil, ErrRouteNotReady
	}
	routes := make([]Route, len(keys))
	for i, key := range keys {
		route, hashSlot, err := routeKey(table, key)
		if err != nil {
			return nil, fmt.Errorf("route key index=%d key=%q hashSlot=%d: %w", i, key, hashSlot, err)
		}
		routes[i] = route
	}
	return routes, nil
}

// RouteAuthorities routes keys through one current table snapshot and returns
// only authority fence fields in input order. Unlike RouteKeys, it does not
// clone Slot peer slices for each key.
func (r *Router) RouteAuthorities(keys []string) ([]AuthorityRoute, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	table := r.Table()
	if table == nil {
		return nil, ErrRouteNotReady
	}
	return table.RouteAuthorities(keys)
}

// RouteAuthoritiesPartial routes every key through one current table snapshot
// and returns aligned lightweight authority results. The outer error reports
// only a missing route table; key-specific failures stay in their result.
func (r *Router) RouteAuthoritiesPartial(keys []string) ([]AuthorityRouteResult, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	table := r.Table()
	if table == nil {
		return nil, ErrRouteNotReady
	}
	return table.RouteAuthoritiesPartial(keys)
}

// RouteKeysPartial routes every key through one current table snapshot and preserves aligned failures.
// The outer error reports only that no routing table is installed; key-specific failures stay in the result.
func (r *Router) RouteKeysPartial(keys []string) ([]RouteKeyResult, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	table := r.Table()
	if table == nil {
		return nil, ErrRouteNotReady
	}
	results := make([]RouteKeyResult, len(keys))
	for i, key := range keys {
		route, hashSlot, err := routeKey(table, key)
		if err != nil {
			results[i].Err = fmt.Errorf("route key index=%d key=%q hashSlot=%d: %w", i, key, hashSlot, err)
			continue
		}
		results[i].Route = route
	}
	return results, nil
}

func routeKey(table *Table, key string) (Route, uint16, error) {
	hashSlot := HashSlotForKey(key, table.HashSlotCount)
	route, err := table.routeHashSlot(hashSlot)
	return route, hashSlot, err
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
				table.SlotLeaderTerms[slotID] = current.SlotLeaderTerms[slotID]
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

// AdvanceRevision publishes a newer control revision without rebuilding unchanged topology.
func (r *Router) AdvanceRevision(revision uint64) {
	if r == nil || revision == 0 {
		return
	}
	for {
		current := r.current.Load()
		if current == nil || current.Revision >= revision {
			return
		}
		next := current.cloneWithRevision(revision)
		if r.current.CompareAndSwap(current, next) {
			return
		}
	}
}
