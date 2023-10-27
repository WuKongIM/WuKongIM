package cluster

import "sync"

type NodeManager struct {
	clientMap     map[uint64]*NodeClient
	clientMapLock sync.RWMutex
}

func NewNodeManager() *NodeManager {
	return &NodeManager{
		clientMap: make(map[uint64]*NodeClient),
	}
}

func (n *NodeManager) Add(cli *NodeClient) {
	n.clientMapLock.Lock()
	defer n.clientMapLock.Unlock()
	n.clientMap[cli.NodeID] = cli
}

func (n *NodeManager) Remove(nodeID uint64) {
	n.clientMapLock.Lock()
	defer n.clientMapLock.Unlock()
	delete(n.clientMap, nodeID)
}
func (n *NodeManager) Get(nodeID uint64) *NodeClient {
	n.clientMapLock.RLock()
	defer n.clientMapLock.RUnlock()
	return n.clientMap[nodeID]
}

func (n *NodeManager) GetAll() []*NodeClient {
	n.clientMapLock.RLock()
	defer n.clientMapLock.RUnlock()
	clients := make([]*NodeClient, 0)
	for _, cli := range n.clientMap {
		clients = append(clients, cli)
	}
	return clients
}
