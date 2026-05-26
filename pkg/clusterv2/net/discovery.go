package clusternet

import (
	"fmt"
	"sync/atomic"
)

// NodeAddress describes one node address visible to clusterv2 RPC discovery.
type NodeAddress struct {
	// NodeID is the stable non-zero node identity.
	NodeID uint64
	// Addr is the node RPC address.
	Addr string
}

// Discovery stores an immutable nodeID -> address snapshot.
type Discovery struct {
	current atomic.Pointer[map[uint64]string]
}

// NewDiscovery creates an empty Discovery.
func NewDiscovery() *Discovery {
	d := &Discovery{}
	empty := make(map[uint64]string)
	d.current.Store(&empty)
	return d
}

// Update replaces the discovery snapshot with addresses from nodes.
func (d *Discovery) Update(nodes []NodeAddress) {
	next := make(map[uint64]string, len(nodes))
	for _, node := range nodes {
		if node.NodeID == 0 || node.Addr == "" {
			continue
		}
		next[node.NodeID] = node.Addr
	}
	d.current.Store(&next)
}

// Addr returns the address for nodeID from the current snapshot.
func (d *Discovery) Addr(nodeID uint64) (string, bool) {
	if d == nil {
		return "", false
	}
	current := d.current.Load()
	if current == nil {
		return "", false
	}
	addr, ok := (*current)[nodeID]
	return addr, ok
}

// Resolve implements pkg/transport Discovery.
func (d *Discovery) Resolve(nodeID uint64) (string, error) {
	addr, ok := d.Addr(nodeID)
	if !ok {
		return "", fmt.Errorf("%w: node %d", ErrNodeNotFound, nodeID)
	}
	return addr, nil
}

// Snapshot returns a copy of the current discovery map.
func (d *Discovery) Snapshot() map[uint64]string {
	out := make(map[uint64]string)
	if d == nil {
		return out
	}
	current := d.current.Load()
	if current == nil {
		return out
	}
	for nodeID, addr := range *current {
		out[nodeID] = addr
	}
	return out
}
