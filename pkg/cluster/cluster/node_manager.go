package cluster

import (
	"sync"
)

type nodeManager struct {
	nodeMap map[uint64]*node
	opts    *Options
	mu      sync.RWMutex
}

func newNodeManager(opts *Options) *nodeManager {
	return &nodeManager{
		nodeMap: make(map[uint64]*node),
		opts:    opts,
	}
}

func (n *nodeManager) addNode(nd *node) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.nodeMap[nd.id] = nd
}

func (n *nodeManager) removeNode(id uint64) {
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.nodeMap, id)
}

func (n *nodeManager) node(id uint64) *node {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.nodeMap[id]
}

func (n *nodeManager) requesting() int64 {
	nodes := n.nodes()
	var count int64 = 0
	for _, node := range nodes {
		count += node.requesting()
	}
	return count
}

func (n *nodeManager) sending() int64 {
	nodes := n.nodes()
	var count int64 = 0
	for _, node := range nodes {
		count += node.sending()
	}
	return count
}

func (n *nodeManager) nodes() []*node {
	n.mu.RLock()
	defer n.mu.RUnlock()
	var nodes []*node
	for _, node := range n.nodeMap {
		nodes = append(nodes, node)
	}
	return nodes
}

func (n *nodeManager) stop() {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, node := range n.nodeMap {
		node.stop()
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
