package cluster

import (
	"context"
	"fmt"
	"sync"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

// dataNodeView stores the latest schedulable data-node IDs from control snapshots.
type dataNodeView struct {
	mu       sync.RWMutex
	revision uint64
	nodes    []uint64
	changed  chan struct{}
}

// UpdateAtRevision atomically replaces the placement candidates and the exact
// control revision from which they were derived.
func (v *dataNodeView) UpdateAtRevision(revision uint64, nodes []uint64) {
	v.mu.Lock()
	v.revision = revision
	v.nodes = append([]uint64(nil), nodes...)
	previous := v.changed
	v.changed = make(chan struct{})
	if previous != nil {
		close(previous)
	}
	v.mu.Unlock()
}

// DataNodes returns a defensive copy of the latest data-node set.
func (v *dataNodeView) DataNodes() []uint64 {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return append([]uint64(nil), v.nodes...)
}

// PlacementDataNodes waits for and returns the exact control revision used by
// an already-routed create batch. A newer candidate generation proves the
// supplied route stale and must be rerouted by the caller.
func (v *dataNodeView) PlacementDataNodes(ctx context.Context, expectedRevision uint64) ([]uint64, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		v.mu.Lock()
		switch {
		case v.revision == expectedRevision:
			nodes := append([]uint64(nil), v.nodes...)
			v.mu.Unlock()
			return nodes, nil
		case v.revision > expectedRevision:
			actual := v.revision
			v.mu.Unlock()
			return nil, fmt.Errorf("%w: channel placement route revision=%d candidates=%d", ch.ErrStaleMeta, expectedRevision, actual)
		}
		if v.changed == nil {
			v.changed = make(chan struct{})
		}
		changed := v.changed
		v.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-changed:
		}
	}
}
