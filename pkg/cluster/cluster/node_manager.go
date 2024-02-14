package cluster

import (
	"context"
	"fmt"
)

type nodeManager struct {
	nodeMap map[uint64]*node
	opts    *Options
}

func newNodeManager(opts *Options) *nodeManager {
	return &nodeManager{
		nodeMap: make(map[uint64]*node),
		opts:    opts,
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

func (n *nodeManager) requestSlotLogInfo(to uint64, req *SlotLogInfoReq) (*SlotLogInfoResp, error) {
	node := n.node(to)
	if node == nil {
		return nil, fmt.Errorf("node not found")
	}
	timeoutCtx, cancel := context.WithTimeout(context.Background(), n.opts.ReqTimeout)
	defer cancel()
	return node.requestSlotLogInfo(timeoutCtx, req)
}
