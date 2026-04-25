package cluster

import (
	"sort"
	"sync"
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	slotcontroller "github.com/WuKongIM/WuKongIM/pkg/controller/plane"
)

type nodeObservation struct {
	NodeID               uint64
	Addr                 string
	ObservedAt           time.Time
	CapacityWeight       int
	HashSlotTableVersion uint64
}

type observationSnapshot struct {
	Nodes        []nodeObservation
	RuntimeViews []controllermeta.SlotRuntimeView
}

type observationCache struct {
	mu                 sync.Mutex
	nodes              map[uint64]nodeObservation
	runtimeViewsByNode map[uint64]map[uint32]controllermeta.SlotRuntimeView
	runtimeObservedAt  map[uint64]time.Time
	runtimeReceivedAt  map[uint64]time.Time
	now                func() time.Time
	runtimeViewTTL     time.Duration
}

func newObservationCache() *observationCache {
	return &observationCache{
		nodes:              make(map[uint64]nodeObservation),
		runtimeViewsByNode: make(map[uint64]map[uint32]controllermeta.SlotRuntimeView),
		runtimeObservedAt:  make(map[uint64]time.Time),
		runtimeReceivedAt:  make(map[uint64]time.Time),
		now:                time.Now,
	}
}

func (c *observationCache) applyNodeReport(report slotcontroller.AgentReport) {
	if c == nil || report.NodeID == 0 {
		return
	}
	observation := nodeObservation{
		NodeID:               report.NodeID,
		Addr:                 report.Addr,
		ObservedAt:           report.ObservedAt,
		CapacityWeight:       report.CapacityWeight,
		HashSlotTableVersion: report.HashSlotTableVersion,
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if current, ok := c.nodes[report.NodeID]; ok && observation.ObservedAt.Before(current.ObservedAt) {
		return
	}
	c.nodes[report.NodeID] = observation
}

func (c *observationCache) applyRuntimeReport(report runtimeObservationReport) {
	if c == nil || report.NodeID == 0 {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	receivedAt := time.Now()
	if c.now != nil {
		receivedAt = c.now()
	}
	if lastObservedAt, ok := c.runtimeObservedAt[report.NodeID]; ok && report.ObservedAt.Before(lastObservedAt) {
		return
	}

	nodeViews := c.runtimeViewsByNode[report.NodeID]
	if report.FullSync || nodeViews == nil {
		nodeViews = make(map[uint32]controllermeta.SlotRuntimeView, len(report.Views))
		c.runtimeViewsByNode[report.NodeID] = nodeViews
	}

	if report.FullSync {
		for _, view := range report.Views {
			if view.SlotID == 0 {
				continue
			}
			nodeViews[view.SlotID] = cloneRuntimeView(view)
		}
		c.runtimeObservedAt[report.NodeID] = report.ObservedAt
		c.runtimeReceivedAt[report.NodeID] = receivedAt
		return
	}

	for _, view := range report.Views {
		if view.SlotID == 0 {
			continue
		}
		current, ok := nodeViews[view.SlotID]
		if ok && view.LastReportAt.Before(current.LastReportAt) {
			continue
		}
		if ok && runtimeViewEquivalent(current, view) {
			current.LastReportAt = view.LastReportAt
			nodeViews[view.SlotID] = current
			continue
		}
		nodeViews[view.SlotID] = cloneRuntimeView(view)
	}
	for _, slotID := range report.ClosedSlots {
		delete(nodeViews, slotID)
	}
	c.runtimeObservedAt[report.NodeID] = report.ObservedAt
	c.runtimeReceivedAt[report.NodeID] = receivedAt
}

func (c *observationCache) snapshot() observationSnapshot {
	if c == nil {
		return observationSnapshot{}
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.evictStaleRuntimeViewsLocked()

	snapshot := observationSnapshot{
		Nodes: make([]nodeObservation, 0, len(c.nodes)),
	}
	for _, node := range c.nodes {
		snapshot.Nodes = append(snapshot.Nodes, node)
	}
	snapshot.RuntimeViews = c.snapshotRuntimeViewsLocked()
	sort.Slice(snapshot.Nodes, func(i, j int) bool {
		return snapshot.Nodes[i].NodeID < snapshot.Nodes[j].NodeID
	})
	return snapshot
}

// snapshotRuntimeViews returns the latest slot-scoped runtime view aggregation.
func (c *observationCache) snapshotRuntimeViews() []controllermeta.SlotRuntimeView {
	if c == nil {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.evictStaleRuntimeViewsLocked()
	return c.snapshotRuntimeViewsLocked()
}

func (c *observationCache) reset() {
	if c == nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.nodes = make(map[uint64]nodeObservation)
	c.runtimeViewsByNode = make(map[uint64]map[uint32]controllermeta.SlotRuntimeView)
	c.runtimeObservedAt = make(map[uint64]time.Time)
	c.runtimeReceivedAt = make(map[uint64]time.Time)
}

func (c *observationCache) evictStaleRuntimeViewsLocked() {
	if c == nil || c.runtimeViewTTL <= 0 {
		return
	}
	now := time.Now()
	if c.now != nil {
		now = c.now()
	}
	for nodeID, receivedAt := range c.runtimeReceivedAt {
		if receivedAt.IsZero() || now.Sub(receivedAt) <= c.runtimeViewTTL {
			continue
		}
		delete(c.runtimeObservedAt, nodeID)
		delete(c.runtimeReceivedAt, nodeID)
		delete(c.runtimeViewsByNode, nodeID)
	}
}

func (c *observationCache) snapshotRuntimeViewsLocked() []controllermeta.SlotRuntimeView {
	runtimeViews := make(map[uint32]controllermeta.SlotRuntimeView)
	for _, nodeViews := range c.runtimeViewsByNode {
		for slotID, view := range nodeViews {
			current, ok := runtimeViews[slotID]
			if ok && current.LastReportAt.After(view.LastReportAt) {
				continue
			}
			runtimeViews[slotID] = cloneRuntimeView(view)
		}
	}

	out := make([]controllermeta.SlotRuntimeView, 0, len(runtimeViews))
	for _, view := range runtimeViews {
		out = append(out, view)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].SlotID < out[j].SlotID
	})
	return out
}

func runtimeViewEquivalent(left, right controllermeta.SlotRuntimeView) bool {
	if left.SlotID != right.SlotID ||
		left.LeaderID != right.LeaderID ||
		left.HealthyVoters != right.HealthyVoters ||
		left.HasQuorum != right.HasQuorum ||
		left.ObservedConfigEpoch != right.ObservedConfigEpoch ||
		len(left.CurrentPeers) != len(right.CurrentPeers) {
		return false
	}
	for i := range left.CurrentPeers {
		if left.CurrentPeers[i] != right.CurrentPeers[i] {
			return false
		}
	}
	return true
}

func cloneUint64Slice(src []uint64) []uint64 {
	if len(src) == 0 {
		return nil
	}
	dst := make([]uint64, len(src))
	copy(dst, src)
	return dst
}
