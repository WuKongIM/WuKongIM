package cluster

import (
	"sync"
)

type nodeManager struct {
	nodeMap map[uint64]*ImprovedNode
	opts    *Options
	mu      sync.RWMutex
}

func newNodeManager(opts *Options) *nodeManager {
	return &nodeManager{
		nodeMap: make(map[uint64]*ImprovedNode),
		opts:    opts,
	}
}

func (n *nodeManager) addNode(nd *ImprovedNode) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.nodeMap[nd.id] = nd
}

func (n *nodeManager) removeNode(id uint64) {
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.nodeMap, id)
}

func (n *nodeManager) node(id uint64) *ImprovedNode {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.nodeMap[id]
}

func (n *nodeManager) nodes() []*ImprovedNode {
	n.mu.RLock()
	defer n.mu.RUnlock()
	var nodes []*ImprovedNode
	for _, node := range n.nodeMap {
		nodes = append(nodes, node)
	}
	return nodes
}

func (n *nodeManager) stop() {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, node := range n.nodeMap {
		node.Stop()
	}

}

func (n *nodeManager) exist(id uint64) bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	if _, ok := n.nodeMap[id]; ok {
		return true
	}
	return false
}
