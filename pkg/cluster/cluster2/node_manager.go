package cluster

import (
	"context"
	"fmt"
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

func (n *nodeManager) outboundFlightMessageCount() int64 {
	n.mu.RLock()
	defer n.mu.RUnlock()

	var count int64
	for _, node := range n.nodeMap {
		count += node.outboundFlightMessageCount()
	}
	return count
}

func (n *nodeManager) outboundFlightMessageBytes() int64 {

	n.mu.RLock()
	defer n.mu.RUnlock()

	var count int64
	for _, node := range n.nodeMap {
		count += node.outboundFlightMessageBytes()
	}
	return count
}

func (n *nodeManager) exist(id uint64) bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	if _, ok := n.nodeMap[id]; ok {
		return true
	}
	return false
}

func (n *nodeManager) requestSlotLogInfo(to uint64, req *SlotLogInfoReq) (*SlotLogInfoResp, error) {
	node := n.node(to)
	if node == nil {
		return nil, fmt.Errorf("node[%d] not found", to)
	}
	timeoutCtx, cancel := context.WithTimeout(context.Background(), n.opts.ReqTimeout)
	defer cancel()
	return node.requestSlotLogInfo(timeoutCtx, req)
}

func (n *nodeManager) requestClusterJoin(to uint64, req *ClusterJoinReq) (*ClusterJoinResp, error) {
	node := n.node(to)
	if node == nil {
		return nil, fmt.Errorf("node[%d] not found", to)
	}
	timeoutCtx, cancel := context.WithTimeout(context.Background(), n.opts.ReqTimeout)
	defer cancel()
	return node.requestClusterJoin(timeoutCtx, req)
}

func (n *nodeManager) requestSlotMigrateFinished(to uint64, req *SlotMigrateFinishReq) error {
	node := n.node(to)
	if node == nil {
		return fmt.Errorf("node[%d] not found", to)
	}
	timeoutCtx, cancel := context.WithTimeout(context.Background(), n.opts.ReqTimeout)
	defer cancel()
	return node.requestSlotMigrateFinished(timeoutCtx, req)
}
