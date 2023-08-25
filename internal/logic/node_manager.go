package logic

import (
	"sync"
)

type nodeManager struct {
	nodeMap     map[string]*node
	nodeMapLock sync.RWMutex
}

func newNodeManager() *nodeManager {
	return &nodeManager{
		nodeMap: make(map[string]*node),
	}
}

func (m *nodeManager) add(n *node) {
	m.nodeMapLock.Lock()
	defer m.nodeMapLock.Unlock()
	m.nodeMap[n.nodeID()] = n
}

func (m *nodeManager) remove(n *node) {
	m.nodeMapLock.Lock()
	defer m.nodeMapLock.Unlock()
	delete(m.nodeMap, n.nodeID())
}

func (m *nodeManager) get(uid string) *node {
	m.nodeMapLock.RLock()
	defer m.nodeMapLock.RUnlock()
	return m.nodeMap[uid]
}

func (m *nodeManager) getAllNode() []*node {
	m.nodeMapLock.RLock()
	defer m.nodeMapLock.RUnlock()
	conns := make([]*node, 0, len(m.nodeMap))
	for _, conn := range m.nodeMap {
		conns = append(conns, conn)
	}
	return conns
}
