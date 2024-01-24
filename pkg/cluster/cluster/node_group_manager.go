package cluster

type nodeGroupManager struct {
}

func newNodeGroupManager() *nodeGroupManager {
	return &nodeGroupManager{}
}

func (n *nodeGroupManager) addNode(nd *node) {
}

func (n *nodeGroupManager) removeNode(id uint64) {
}

func (n *nodeGroupManager) node(id uint64) *node {
	return nil
}

func (n *nodeGroupManager) nodes() []*node {
	return nil
}

func (n *nodeGroupManager) exist(id uint64) bool {
	return false
}
