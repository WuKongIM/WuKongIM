package cluster

import "sync"

type nodeManager struct {
	sync.RWMutex
	nodes map[uint64]*node
}

func newNodeManager() *nodeManager {
	return &nodeManager{
		nodes: make(map[uint64]*node),
	}
}

func (n *nodeManager) addNode(node *node) {
	n.Lock()
	defer n.Unlock()
	n.nodes[node.id] = node
}

func (n *nodeManager) removeNode(nodeID uint64) {
	n.Lock()
	defer n.Unlock()
	delete(n.nodes, nodeID)
}

func (n *nodeManager) getNode(nodeID uint64) *node {
	n.RLock()
	defer n.RUnlock()
	return n.nodes[nodeID]
}

func (n *nodeManager) getAllNode() []*node {
	n.RLock()
	defer n.RUnlock()
	nodes := make([]*node, 0, len(n.nodes))
	for _, node := range n.nodes {
		nodes = append(nodes, node)
	}
	return nodes
}
