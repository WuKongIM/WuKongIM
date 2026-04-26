package channelmeta

import (
	"context"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"golang.org/x/sync/singleflight"
)

const nodeLivenessSingleflightKey = "controller_nodes"

// LivenessCache stores controller-observed node liveness for channel metadata decisions.
type LivenessCache struct {
	mu     sync.Mutex
	values map[uint64]controllermeta.NodeStatus
	sf     singleflight.Group
}

// Update stores the latest known status and reports whether unhealthy state should trigger refresh.
func (c *LivenessCache) Update(nodeID uint64, status controllermeta.NodeStatus) bool {
	if c == nil || nodeID == 0 {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.values == nil {
		c.values = make(map[uint64]controllermeta.NodeStatus)
	}
	previous, ok := c.values[nodeID]
	c.values[nodeID] = status
	return (status == controllermeta.NodeStatusDead || status == controllermeta.NodeStatusDraining) && (!ok || previous != status)
}

// Status returns the cached status for a node.
func (c *LivenessCache) Status(nodeID uint64) (controllermeta.NodeStatus, bool) {
	if c == nil || nodeID == 0 {
		return controllermeta.NodeStatusUnknown, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	status, ok := c.values[nodeID]
	return status, ok
}

// Warm populates the cache from a strict liveness source when nodeID is not cached.
func (c *LivenessCache) Warm(ctx context.Context, nodeID uint64, source LivenessSource) {
	if c == nil || nodeID == 0 || source == nil {
		return
	}
	if _, ok := c.Status(nodeID); ok {
		return
	}
	value, err, _ := c.sf.Do(nodeLivenessSingleflightKey, func() (any, error) {
		nodes, err := source.ListNodesStrict(ctx)
		if err != nil {
			return nil, err
		}
		c.StoreSnapshot(nodes)
		return nodes, nil
	})
	if err != nil {
		return
	}
	nodes, ok := value.([]controllermeta.ClusterNode)
	if !ok {
		return
	}
	c.StoreSnapshot(nodes)
}

// StoreSnapshot stores a controller node snapshot in the cache.
func (c *LivenessCache) StoreSnapshot(nodes []controllermeta.ClusterNode) {
	if c == nil || len(nodes) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.values == nil {
		c.values = make(map[uint64]controllermeta.NodeStatus, len(nodes))
	}
	for _, node := range nodes {
		if node.NodeID == 0 {
			continue
		}
		c.values[node.NodeID] = node.Status
	}
}

// ObservedLeaderRepairReason reports local replica leader drift observed from runtime state.
func ObservedLeaderRepairReason(meta channel.Meta, state channel.ReplicaState) string {
	if meta.Status != channel.StatusActive || state.Role == channel.ReplicaRoleTombstoned {
		return ""
	}
	if state.Leader == 0 || state.Leader == meta.Leader {
		return ""
	}
	if state.Epoch != 0 && meta.Epoch != 0 && state.Epoch != meta.Epoch {
		return ""
	}
	if !containsNodeID(meta.ISR, state.Leader) {
		return ""
	}
	return channel.LeaderRepairReasonLeaderDrift.String()
}

func containsNodeID(values []channel.NodeID, target channel.NodeID) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
