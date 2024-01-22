package cluster

type NodeGroupManager struct {
}

func NewNodeGroupManager() *NodeGroupManager {
	return &NodeGroupManager{}
}

func (n *NodeGroupManager) AddNode(nd *Node) {
}

func (n *NodeGroupManager) RemoveNode(nd *Node) {
}

func (n *NodeGroupManager) Node(id uint64) *Node {
	return nil
}

func (n *NodeGroupManager) Nodes() []*Node {
	return nil
}

func (n *NodeGroupManager) Exist(id uint64) bool {
	return false
}
