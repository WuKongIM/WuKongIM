package clusterset

import "sync"

type nodeManager struct {
	nodeMap     map[uint64]*node
	nodeMapLock sync.RWMutex
}

func newNodeManager() *nodeManager {
	return &nodeManager{
		nodeMap: make(map[uint64]*node),
	}
}

func (n *nodeManager) addNode(nd *node) {
	n.nodeMapLock.Lock()
	defer n.nodeMapLock.Unlock()
	n.nodeMap[nd.nodeID] = nd
}

func (n *nodeManager) removeNode(nodeID uint64) {
	n.nodeMapLock.Lock()
	defer n.nodeMapLock.Unlock()
	delete(n.nodeMap, nodeID)
}

func (n *nodeManager) getNode(nodeID uint64) *node {
	n.nodeMapLock.RLock()
	defer n.nodeMapLock.RUnlock()
	return n.nodeMap[nodeID]
}

func (n *nodeManager) getAllNode() []*node {

	n.nodeMapLock.RLock()
	defer n.nodeMapLock.RUnlock()

	var nodes []*node
	for _, node := range n.nodeMap {
		nodes = append(nodes, node)
	}
	return nodes
}
