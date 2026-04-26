package cluster

import (
	"sort"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

// SeedConfig identifies a bootstrap peer or temporary leader hint.
type SeedConfig struct {
	// ID is the node identifier for the seed endpoint.
	ID multiraft.NodeID
	// Addr is the transport address advertised by the seed endpoint.
	Addr string
}

// DynamicDiscovery resolves nodes from the latest controller snapshot with seed fallback.
type DynamicDiscovery struct {
	mu sync.RWMutex
	// seeds holds bootstrap endpoints and temporary leader hints.
	seeds map[uint64]NodeInfo
	// nodes holds the latest authoritative dynamic membership snapshot.
	nodes map[uint64]NodeInfo
	// subscribers receive address-change events after the snapshot lock is released.
	subscribers    map[uint64]func(nodeID uint64, oldAddr, newAddr string)
	nextSubscriber uint64
	stopped        bool
}

type discoveryAddressChange struct {
	nodeID  uint64
	oldAddr string
	newAddr string
}

// NewDynamicDiscovery builds a discovery source from bootstrap seeds and an optional node snapshot.
func NewDynamicDiscovery(seeds []SeedConfig, nodes []NodeConfig) *DynamicDiscovery {
	d := &DynamicDiscovery{
		seeds:       make(map[uint64]NodeInfo, len(seeds)),
		nodes:       make(map[uint64]NodeInfo, len(nodes)),
		subscribers: make(map[uint64]func(nodeID uint64, oldAddr, newAddr string)),
	}
	for _, seed := range seeds {
		d.seeds[uint64(seed.ID)] = NodeInfo{NodeID: seed.ID, Addr: seed.Addr}
	}
	for _, node := range nodes {
		d.nodes[uint64(node.NodeID)] = NodeInfo{NodeID: node.NodeID, Addr: node.Addr}
	}
	return d
}

// GetNodes returns a deterministic union of dynamic nodes and seed fallbacks.
func (d *DynamicDiscovery) GetNodes() []NodeInfo {
	d.mu.RLock()
	defer d.mu.RUnlock()

	merged := make(map[uint64]NodeInfo, len(d.seeds)+len(d.nodes))
	for nodeID, node := range d.seeds {
		merged[nodeID] = node
	}
	for nodeID, node := range d.nodes {
		merged[nodeID] = node
	}
	out := make([]NodeInfo, 0, len(merged))
	for _, node := range merged {
		out = append(out, node)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].NodeID < out[j].NodeID
	})
	return out
}

// Resolve returns the latest dynamic address first, then falls back to seed hints.
func (d *DynamicDiscovery) Resolve(nodeID uint64) (string, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if node, ok := d.nodes[nodeID]; ok {
		return node.Addr, nil
	}
	if node, ok := d.seeds[nodeID]; ok {
		return node.Addr, nil
	}
	return "", transport.ErrNodeNotFound
}

// UpsertSeed records a bootstrap seed without replacing the dynamic node snapshot.
func (d *DynamicDiscovery) UpsertSeed(seed SeedConfig) {
	nodeID := uint64(seed.ID)
	var changes []discoveryAddressChange
	var subscribers []func(nodeID uint64, oldAddr, newAddr string)

	d.mu.Lock()
	oldAddr, oldOK := d.effectiveAddrLocked(nodeID, d.nodes)
	d.seeds[nodeID] = NodeInfo{NodeID: seed.ID, Addr: seed.Addr}
	newAddr, newOK := d.effectiveAddrLocked(nodeID, d.nodes)
	if oldOK && newOK && oldAddr != newAddr {
		changes = append(changes, discoveryAddressChange{nodeID: nodeID, oldAddr: oldAddr, newAddr: newAddr})
	}
	if len(changes) > 0 && !d.stopped {
		subscribers = d.subscribersLocked()
	}
	d.mu.Unlock()

	notifyDiscoveryAddressChanges(changes, subscribers)
}

// UpdateNodes replaces the dynamic node snapshot and reports effective address changes.
func (d *DynamicDiscovery) UpdateNodes(nodes []NodeConfig) (changed []uint64) {
	next := make(map[uint64]NodeInfo, len(nodes))
	for _, node := range nodes {
		next[uint64(node.NodeID)] = NodeInfo{NodeID: node.NodeID, Addr: node.Addr}
	}

	var changes []discoveryAddressChange
	var subscribers []func(nodeID uint64, oldAddr, newAddr string)

	d.mu.Lock()
	affected := make(map[uint64]struct{}, len(d.nodes)+len(next)+len(d.seeds))
	for nodeID := range d.nodes {
		affected[nodeID] = struct{}{}
	}
	for nodeID := range next {
		affected[nodeID] = struct{}{}
	}
	for nodeID := range d.seeds {
		affected[nodeID] = struct{}{}
	}
	for nodeID := range affected {
		oldAddr, oldOK := d.effectiveAddrLocked(nodeID, d.nodes)
		newAddr, newOK := d.effectiveAddrLocked(nodeID, next)
		if oldOK != newOK || oldAddr != newAddr {
			changed = append(changed, nodeID)
			changes = append(changes, discoveryAddressChange{nodeID: nodeID, oldAddr: oldAddr, newAddr: newAddr})
		}
	}
	d.nodes = next
	if len(changes) > 0 && !d.stopped {
		subscribers = d.subscribersLocked()
	}
	d.mu.Unlock()

	sort.Slice(changed, func(i, j int) bool { return changed[i] < changed[j] })
	notifyDiscoveryAddressChanges(changes, subscribers)
	return changed
}

func (d *DynamicDiscovery) effectiveAddrLocked(nodeID uint64, nodes map[uint64]NodeInfo) (string, bool) {
	if node, ok := nodes[nodeID]; ok {
		return node.Addr, true
	}
	if node, ok := d.seeds[nodeID]; ok {
		return node.Addr, true
	}
	return "", false
}

func (d *DynamicDiscovery) subscribersLocked() []func(nodeID uint64, oldAddr, newAddr string) {
	subscribers := make([]func(nodeID uint64, oldAddr, newAddr string), 0, len(d.subscribers))
	for _, fn := range d.subscribers {
		subscribers = append(subscribers, fn)
	}
	return subscribers
}

func notifyDiscoveryAddressChanges(changes []discoveryAddressChange, subscribers []func(nodeID uint64, oldAddr, newAddr string)) {
	sort.Slice(changes, func(i, j int) bool { return changes[i].nodeID < changes[j].nodeID })
	for _, change := range changes {
		for _, fn := range subscribers {
			fn(change.nodeID, change.oldAddr, change.newAddr)
		}
	}
}

// OnAddressChange registers a callback for transport pools that need to evict stale peers.
func (d *DynamicDiscovery) OnAddressChange(fn func(nodeID uint64, oldAddr, newAddr string)) func() {
	if fn == nil {
		return func() {}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopped {
		return func() {}
	}
	d.nextSubscriber++
	id := d.nextSubscriber
	d.subscribers[id] = fn
	return func() {
		d.mu.Lock()
		defer d.mu.Unlock()
		delete(d.subscribers, id)
	}
}

// Stop drops subscribers while leaving the last discovery snapshot readable.
func (d *DynamicDiscovery) Stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stopped = true
	d.subscribers = nil
}
