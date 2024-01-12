package cluster

import (
	"context"
	"fmt"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/clusterevent/pb"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
)

type nodeManager struct {
	sync.RWMutex
	nodes map[uint64]*node
}

func newNodeManager() *nodeManager {
	return &nodeManager{
		nodes: make(map[uint64]*node),
	}
}

func (n *nodeManager) addNode(node *node) {
	n.Lock()
	defer n.Unlock()
	n.nodes[node.id] = node
}

func (n *nodeManager) removeNode(nodeID uint64) {
	n.Lock()
	defer n.Unlock()
	delete(n.nodes, nodeID)
}

func (n *nodeManager) getNode(nodeID uint64) *node {
	n.RLock()
	defer n.RUnlock()
	return n.nodes[nodeID]
}

func (n *nodeManager) getAllNode() []*node {
	n.RLock()
	defer n.RUnlock()
	nodes := make([]*node, 0, len(n.nodes))
	for _, node := range n.nodes {
		nodes = append(nodes, node)
	}
	return nodes
}

// 获取所有投票节点
func (n *nodeManager) getAllVoteNodes() []*node {
	n.RLock()
	defer n.RUnlock()
	nodes := make([]*node, 0, len(n.nodes))
	for _, node := range n.nodes {
		if node.allowVote {
			nodes = append(nodes, node)
		}
	}
	return nodes
}

func (n *nodeManager) send(nodeID uint64, msg *proto.Message) error {
	node := n.getNode(nodeID)
	if node != nil {
		return node.send(msg)
	}
	return fmt.Errorf("node[%d] not exist", nodeID)
}

// 节点领导发送ping给其他节点
func (n *nodeManager) sendPing(nodeID uint64, req *PingRequest) error {
	node := n.getNode(nodeID)
	if node != nil {
		return node.sendPing(req)
	}
	return fmt.Errorf("node[%d] not exist", nodeID)
}

// 发送投票请求
func (n *nodeManager) sendVote(nodeID uint64, req *VoteRequest) error {
	node := n.getNode(nodeID)
	if node != nil {
		return node.sendVote(req)
	}
	return fmt.Errorf("node[%d] not exist", nodeID)
}

// 投票结果返回
func (n *nodeManager) sendVoteResp(nodeID uint64, req *VoteResponse) error {
	node := n.getNode(nodeID)
	if node != nil {
		return node.sendVoteResp(req)
	}
	return fmt.Errorf("node[%d] not exist", nodeID)
}

// 回应节点领导的ping
func (n *nodeManager) sendPong(nodeID uint64, req *PongResponse) error {
	node := n.getNode(nodeID)
	if node != nil {
		return node.sendPong(req)
	}
	return fmt.Errorf("node[%d] not exist", nodeID)
}

// 请求分布式配置
func (n *nodeManager) requestClusterConfig(ctx context.Context, nodeID uint64) (*pb.Cluster, error) {
	node := n.getNode(nodeID)
	if node != nil {
		return node.requestClusterConfig(ctx)
	}
	return nil, fmt.Errorf("node[%d] not exist", nodeID)
}

// 请求节点指定的slot的日志信息
func (n *nodeManager) requestSlotLogInfo(ctx context.Context, nodeID uint64, req *SlotLogInfoReportRequest) (*SlotLogInfoReportResponse, error) {
	node := n.getNode(nodeID)
	if node != nil {
		return node.requestSlotLogInfo(ctx, req)
	}
	return nil, fmt.Errorf("node[%d] not exist", nodeID)
}

// 请求节点指定的channel的日志信息
func (n *nodeManager) requestChannelLogInfo(ctx context.Context, nodeID uint64, req *ChannelLogInfoReportRequest) (*ChannelLogInfoReportResponse, error) {
	node := n.getNode(nodeID)
	if node != nil {
		return node.requestChannelLogInfo(ctx, req)
	}
	return nil, fmt.Errorf("node[%d] not exist", nodeID)
}

// 发送slot同步通知
func (n *nodeManager) sendSlotSyncNotify(nodeID uint64, req *replica.SyncNotify) error {
	node := n.getNode(nodeID)
	if node != nil {
		return node.sendSlotSyncNotify(req)
	}
	return fmt.Errorf("node[%d] not exist", nodeID)
}

// 发送channel同步通知(元数据)
func (n *nodeManager) sendChannelMetaLogSyncNotify(nodeID uint64, req *replica.SyncNotify) error {
	node := n.getNode(nodeID)
	if node != nil {
		return node.sendChannelMetaLogSyncNotify(req)
	}
	return fmt.Errorf("node[%d] not exist", nodeID)
}

// 发送channel同步通知(消息数据)
func (n *nodeManager) sendChannelMessageLogSyncNotify(nodeID uint64, req *replica.SyncNotify) error {
	node := n.getNode(nodeID)
	if node != nil {
		return node.sendChannelMessageLogSyncNotify(req)
	}
	return fmt.Errorf("node[%d] not exist", nodeID)
}

// 请求slot同步日志
func (n *nodeManager) requestSlotSyncLog(ctx context.Context, nodeID uint64, r *replica.SyncReq) (*replica.SyncRsp, error) {
	node := n.getNode(nodeID)
	if node != nil {
		return node.requestSlotSyncLog(ctx, r)
	}
	return nil, fmt.Errorf("node[%d] not exist", nodeID)
}

// 请求channel同步日志
func (n *nodeManager) requestChannelMetaSyncLog(ctx context.Context, nodeID uint64, r *replica.SyncReq) (*replica.SyncRsp, error) {
	node := n.getNode(nodeID)
	if node != nil {
		return node.requestChannelMetaSyncLog(ctx, r)
	}
	return nil, fmt.Errorf("node[%d] not exist", nodeID)
}

func (n *nodeManager) requestChannelMessageSyncLog(ctx context.Context, nodeID uint64, r *replica.SyncReq) (*replica.SyncRsp, error) {
	node := n.getNode(nodeID)
	if node != nil {
		return node.requestChannelMessageSyncLog(ctx, r)
	}
	return nil, fmt.Errorf("node[%d] not exist", nodeID)
}

// 给指定节点发送提案请求
func (n *nodeManager) requestSlotPropose(ctx context.Context, nodeID uint64, req *SlotProposeRequest) error {
	node := n.getNode(nodeID)
	if node != nil {
		return node.requestSlotPropose(ctx, req)
	}
	return fmt.Errorf("node[%d] not exist", nodeID)

}

// 给指定频道发送提案请求
func (n *nodeManager) requestChannelMetaPropose(ctx context.Context, nodeID uint64, req *ChannelProposeRequest) error {
	node := n.getNode(nodeID)
	if node != nil {
		return node.requestChannelMetaPropose(ctx, req)
	}
	return fmt.Errorf("node[%d] not exist", nodeID)
}

func (n *nodeManager) requestChannelMessagePropose(ctx context.Context, nodeID uint64, req *ChannelProposeRequest) error {
	node := n.getNode(nodeID)
	if node != nil {
		return node.requestChannelMessagePropose(ctx, req)
	}
	return fmt.Errorf("node[%d] not exist", nodeID)
}

func (n *nodeManager) requestChannelMessagesPropose(ctx context.Context, nodeID uint64, req *ChannelProposesRequest) error {
	node := n.getNode(nodeID)
	if node != nil {
		return node.requestChannelMessagesPropose(ctx, req)
	}
	return fmt.Errorf("node[%d] not exist", nodeID)
}

// 请求指定频道的集群信息
func (n *nodeManager) requestChannelClusterInfo(ctx context.Context, nodeID uint64, req *ChannelClusterInfoRequest) (*ChannelClusterInfo, error) {
	node := n.getNode(nodeID)
	if node != nil {
		return node.requestChannelClusterInfo(ctx, req)
	}
	return nil, fmt.Errorf("node[%d] not exist", nodeID)
}

// 请求更新节点
func (n *nodeManager) requestNodeUpdate(ctx context.Context, nodeID uint64, nd *pb.Node) error {
	node := n.getNode(nodeID)
	if node != nil {
		return node.requestNodeUpdate(ctx, nd)
	}
	return fmt.Errorf("node[%d] not exist", nodeID)
}

// 频道领导请求副本应用集群信息
func (n *nodeManager) requestApplyClusterInfo(ctx context.Context, nodeID uint64, req *ChannelClusterInfo) error {
	node := n.getNode(nodeID)
	if node != nil {
		return node.requestApplyClusterInfo(ctx, req)
	}
	return fmt.Errorf("node[%d] not exist", nodeID)
}

// 请求频道最后一条日志信息
func (n *nodeManager) requestChannelClusterDetail(ctx context.Context, nodeID uint64, req []*channelClusterDetailoReq) ([]*channelClusterDetailInfo, error) {
	node := n.getNode(nodeID)
	if node != nil {
		return node.requestChannelClusterDetail(ctx, req)
	}
	return nil, fmt.Errorf("node[%d] not exist", nodeID)
}
