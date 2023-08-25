package gateway

import "sync"

type nodeClientManager struct {
	nodeClients     map[string]*nodeClient
	nodeClientsLock sync.RWMutex
}

func newNodeClientManager() *nodeClientManager {
	return &nodeClientManager{
		nodeClients: make(map[string]*nodeClient),
	}
}

func (n *nodeClientManager) addNodeClient(node *nodeClient) {
	n.nodeClientsLock.Lock()
	defer n.nodeClientsLock.Unlock()
	n.nodeClients[node.nodeID] = node
}

func (n *nodeClientManager) removeNodeClient(nodeID string) {
	n.nodeClientsLock.Lock()
	defer n.nodeClientsLock.Unlock()
	delete(n.nodeClients, nodeID)
}

func (n *nodeClientManager) getNodeClient(nodeID string) *nodeClient {
	n.nodeClientsLock.RLock()
	defer n.nodeClientsLock.RUnlock()
	return n.nodeClients[nodeID]
}
