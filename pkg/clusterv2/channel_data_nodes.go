package clusterv2

import "sync"

// dataNodeView stores the latest schedulable data-node IDs from control snapshots.
type dataNodeView struct {
	mu    sync.RWMutex
	nodes []uint64
}

// Update replaces the visible data-node set with a defensive copy.
func (v *dataNodeView) Update(nodes []uint64) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.nodes = append([]uint64(nil), nodes...)
}

// DataNodes returns a defensive copy of the latest data-node set.
func (v *dataNodeView) DataNodes() []uint64 {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return append([]uint64(nil), v.nodes...)
}
