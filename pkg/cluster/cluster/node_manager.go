package cluster

type nodeManager struct {
	nodeMap map[uint64]*node
}

func newNodeManager() *nodeManager {
	return &nodeManager{
		nodeMap: make(map[uint64]*node),
	}
}

func (n *nodeManager) addNode(nd *node) {
	n.nodeMap[nd.id] = nd
}

func (n *nodeManager) removeNode(id uint64) {
	delete(n.nodeMap, id)
}

func (n *nodeManager) node(id uint64) *node {
	return n.nodeMap[id]
}

func (n *nodeManager) nodes() []*node {
	var nodes []*node
	for _, node := range n.nodeMap {
		nodes = append(nodes, node)
	}
	return nodes
}

func (n *nodeManager) exist(id uint64) bool {
	if _, ok := n.nodeMap[id]; ok {
		return true
	}
	return false
}
