package gateway

import (
	"sync"
)

type logicClientManager struct {
	logicClients     map[string]*logicClient
	logicClientsLock sync.RWMutex
	g                *Gateway
}

func newLogicClientManager(g *Gateway) *logicClientManager {
	return &logicClientManager{
		logicClients: make(map[string]*logicClient),
		g:            g,
	}
}

func (n *logicClientManager) addLogicClient(nodeID string, logicClient *logicClient) {
	n.logicClientsLock.Lock()
	defer n.logicClientsLock.Unlock()
	logicClient.start()
	n.logicClients[nodeID] = logicClient
}

func (n *logicClientManager) removeLogicClient(nodeID string) {
	n.logicClientsLock.Lock()
	defer n.logicClientsLock.Unlock()
	cli := n.logicClients[nodeID]
	if cli != nil {
		cli.stop()
	}
	delete(n.logicClients, nodeID)
}

func (n *logicClientManager) getLogicClient(nodeID string) *logicClient {
	n.logicClientsLock.RLock()
	defer n.logicClientsLock.RUnlock()
	return n.logicClients[nodeID]
}

func (n *logicClientManager) getLogicClientWithKey(key string) *logicClient {
	n.logicClientsLock.RLock()
	defer n.logicClientsLock.RUnlock()
	if !n.g.opts.ClusterOn() {
		return n.logicClients[n.g.opts.GatewayID()]
	}
	return n.logicClients[n.getGatewayIDWithKey(key)]
}

func (n *logicClientManager) getGatewayIDWithKey(key string) string {
	n.logicClientsLock.RLock()
	defer n.logicClientsLock.RUnlock()
	if !n.g.opts.ClusterOn() {
		return n.g.opts.GatewayID()
	}

	for gatewayID, _ := range n.logicClients {
		return gatewayID
	}
	return ""
}
