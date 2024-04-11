package cluster

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/cluster/clusterconfig"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"go.uber.org/zap"
)

type ICluster interface {
	Start() error
	Stop()

	// LeaderIdOfChannel 获取channel的leader节点ID
	LeaderIdOfChannel(ctx context.Context, channelID string, channelType uint8) (nodeID uint64, err error)

	// LeaderOfChannel 获取channel的leader节点信息
	LeaderOfChannel(ctx context.Context, channelID string, channelType uint8) (nodeInfo clusterconfig.NodeInfo, err error)

	// SlotLeaderIdOfChannel 获取channel的leader节点信息(不激活频道)
	LeaderOfChannelForRead(channelID string, channelType uint8) (nodeInfo clusterconfig.NodeInfo, err error)

	// SlotLeaderIdOfChannel 获取频道所属槽的领导
	SlotLeaderIdOfChannel(channelID string, channelType uint8) (nodeID uint64, err error)

	// SlotLeaderOfChannel 获取频道所属槽的领导
	SlotLeaderOfChannel(channelID string, channelType uint8) (nodeInfo clusterconfig.NodeInfo, err error)

	// IsSlotLeaderOfChannel 当前节点是否是channel槽的leader节点
	IsSlotLeaderOfChannel(channelID string, channelType uint8) (isLeader bool, err error)

	// IsLeaderNodeOfChannel 当前节点是否是channel的leader节点
	IsLeaderOfChannel(ctx context.Context, channelID string, channelType uint8) (isLeader bool, err error)
	// NodeInfoByID 获取节点信息
	NodeInfoByID(nodeID uint64) (nodeInfo clusterconfig.NodeInfo, err error)
	//Route 设置接受请求的路由
	Route(path string, handler wkserver.Handler)
	// RequestWithContext 发送请求给指定的节点
	RequestWithContext(ctx context.Context, toNodeID uint64, path string, body []byte) (*proto.Response, error)
	// Send 发送消息给指定的节点, MsgType 使用 1000 - 2000之间的值
	Send(toNodeID uint64, msg *proto.Message) error
	// OnMessage 设置接收消息的回调
	OnMessage(f func(msg *proto.Message))
	// 节点是否在线
	NodeIsOnline(nodeID uint64) bool

	Monitor() Monitor
}

func (s *Server) LeaderIdOfChannel(ctx context.Context, channelID string, channelType uint8) (uint64, error) {
	ch, err := s.channelGroupManager.fetchChannel(ctx, channelID, channelType)
	if err != nil {
		return 0, err
	}
	return ch.LeaderId(), nil
}

func (s *Server) LeaderOfChannel(ctx context.Context, channelID string, channelType uint8) (clusterconfig.NodeInfo, error) {
	ch, err := s.channelGroupManager.fetchChannel(ctx, channelID, channelType)
	if err != nil {
		return clusterconfig.EmptyNodeInfo, err
	}
	leaderId := ch.LeaderId()
	if leaderId == 0 {
		s.Error("LeaderOfChannel: leader not found", zap.String("channelID", channelID), zap.Uint8("channelType", channelType))
		return clusterconfig.EmptyNodeInfo, ErrNotLeader

	}
	node := s.clusterEventListener.clusterconfigManager.node(leaderId)
	if node == nil {
		s.Error("LeaderOfChannel: node not found", zap.String("channelID", channelID), zap.Uint8("channelType", channelType), zap.Uint64("leaderId", leaderId))
		return clusterconfig.EmptyNodeInfo, ErrNodeNotFound
	}
	return clusterconfig.NodeInfo{
		Id:            leaderId,
		ApiServerAddr: node.ApiServerAddr,
	}, nil
}

func (s *Server) LeaderOfChannelForRead(channelID string, channelType uint8) (clusterconfig.NodeInfo, error) {
	ch, err := s.channelGroupManager.fetchChannelForRead(channelID, channelType)
	if err != nil {
		return clusterconfig.EmptyNodeInfo, err
	}
	leaderId := ch.LeaderId()
	if leaderId == 0 {
		s.Error("LeaderOfChannel: leader not found", zap.String("channelID", channelID), zap.Uint8("channelType", channelType))
		return clusterconfig.EmptyNodeInfo, ErrNotLeader

	}
	node := s.clusterEventListener.clusterconfigManager.node(leaderId)
	if node == nil {
		s.Error("LeaderOfChannel: node not found", zap.String("channelID", channelID), zap.Uint8("channelType", channelType), zap.Uint64("leaderId", leaderId))
		return clusterconfig.EmptyNodeInfo, ErrNodeNotFound
	}
	return clusterconfig.NodeInfo{
		Id:            leaderId,
		ApiServerAddr: node.ApiServerAddr,
	}, nil
}

func (s *Server) SlotLeaderIdOfChannel(channelID string, channelType uint8) (nodeID uint64, err error) {
	slotId := s.getChannelSlotId(channelID)

	slot := s.clusterEventListener.clusterconfigManager.slot(slotId)
	if slot == nil {
		return 0, ErrSlotNotFound
	}
	return slot.Leader, nil
}

func (s *Server) SlotLeaderOfChannel(channelID string, channelType uint8) (clusterconfig.NodeInfo, error) {
	slotId := s.getChannelSlotId(channelID)

	slot := s.clusterEventListener.clusterconfigManager.slot(slotId)
	if slot == nil {
		s.Error("SlotLeaderOfChannel: slot not found", zap.String("channelID", channelID), zap.Uint8("channelType", channelType), zap.Uint32("slotId", slotId))
		return clusterconfig.EmptyNodeInfo, ErrSlotNotFound
	}
	node := s.clusterEventListener.clusterconfigManager.node(slot.Leader)
	if node == nil {
		s.Error("SlotLeaderOfChannel: node not found", zap.String("channelID", channelID), zap.Uint8("channelType", channelType), zap.Uint32("slotId", slotId), zap.Uint64("leaderId", slot.Leader))
		return clusterconfig.EmptyNodeInfo, ErrNodeNotFound
	}
	return clusterconfig.NodeInfo{
		Id:            slot.Leader,
		ApiServerAddr: node.ApiServerAddr,
	}, nil
}

func (s *Server) IsSlotLeaderOfChannel(channelID string, channelType uint8) (bool, error) {
	slotId := s.getChannelSlotId(channelID)

	slot := s.clusterEventListener.clusterconfigManager.slot(slotId)
	if slot == nil {
		return false, ErrSlotNotFound
	}
	return slot.Leader == s.opts.NodeID, nil
}

func (s *Server) IsLeaderOfChannel(ctx context.Context, channelID string, channelType uint8) (bool, error) {
	ch, err := s.channelGroupManager.fetchChannel(ctx, channelID, channelType)
	if err != nil {
		return false, err
	}
	return ch.IsLeader(), nil
}

func (s *Server) NodeInfoByID(nodeID uint64) (clusterconfig.NodeInfo, error) {
	node := s.clusterEventListener.clusterconfigManager.node(nodeID)
	if node == nil {
		return clusterconfig.EmptyNodeInfo, ErrNodeNotFound
	}
	return clusterconfig.NodeInfo{
		Id:            nodeID,
		ApiServerAddr: node.ApiServerAddr,
	}, nil
}

func (s *Server) Route(path string, handler wkserver.Handler) {
	s.server.Route(path, handler)
}

func (s *Server) RequestWithContext(ctx context.Context, toNodeID uint64, path string, body []byte) (*proto.Response, error) {
	node := s.nodeManager.node(toNodeID)
	if node == nil {
		return nil, ErrNodeNotFound
	}
	return node.requestWithContext(ctx, path, body)
}

func (s *Server) Send(toNodeID uint64, msg *proto.Message) error {
	node := s.nodeManager.node(toNodeID)
	if node == nil {
		return ErrNodeNotFound
	}
	return node.send(msg)
}

func (s *Server) OnMessage(f func(msg *proto.Message)) {
	s.onMessageFnc = f
}

func (s *Server) NodeIsOnline(nodeID uint64) bool {
	return s.clusterEventListener.clusterconfigManager.nodeIsOnline(nodeID)
}

func (s *Server) ProposeChannelMessages(ctx context.Context, channelID string, channelType uint8, logs []replica.Log) (map[uint64]uint64, error) {

	messageItems, err := s.channelGroupManager.proposeMessages(ctx, channelID, channelType, logs)
	if err != nil {
		return nil, err
	}
	messageIdMap := make(map[uint64]uint64)
	for i, item := range messageItems {
		messageIdMap[logs[i].MessageId] = item.messageSeq
	}
	return messageIdMap, nil
}

func (s *Server) ProposeChannelMeta(ctx context.Context, channelID string, channelType uint8, meta []byte) (uint64, error) {
	slotId := s.getChannelSlotId(channelID)
	messageId := uint64(s.messageIDGen.Generate().Int64())
	resultMap, err := s.ProposeToSlot(ctx, slotId, []replica.Log{
		{
			MessageId: messageId,
			Data:      meta,
		},
	})
	if err != nil {
		return 0, nil
	}
	messageSeq, ok := resultMap[messageId]
	if !ok {
		return 0, ErrProposeFailed
	}
	return messageSeq, nil
}

// ProposeToSlot 提交数据到指定的槽
func (s *Server) ProposeToSlot(ctx context.Context, slotId uint32, logs []replica.Log) (map[uint64]uint64, error) {
	slot := s.clusterEventListener.clusterconfigManager.slot(slotId)
	if slot == nil {
		return nil, ErrSlotNotFound
	}
	var messageItems []*messageItem
	var err error
	if slot.Leader != s.opts.NodeID {
		slotLeaderNode := s.nodeManager.node(slot.Leader)
		if slotLeaderNode == nil {
			s.Error("slot leader node not found, ProposeToSlot failed", zap.Uint32("slotId", slotId), zap.Uint64("leaderId", slot.Leader))
			return nil, ErrNodeNotFound
		}
		timeoutCtx, cancel := context.WithTimeout(s.cancelCtx, s.opts.ProposeTimeout)
		defer cancel()
		resp, err := slotLeaderNode.requestSlotPropose(timeoutCtx, &SlotProposeReq{
			SlotId: slotId,
			Logs:   logs,
		})
		if err != nil {
			return nil, err
		}
		messageItems = resp.MessageItems
	} else {
		messageItems, err = s.slotManager.proposeAndWaitCommit(ctx, slotId, logs)
		if err != nil {
			return nil, err
		}
	}

	messageIdMap := make(map[uint64]uint64)
	for i, item := range messageItems {
		messageIdMap[logs[i].MessageId] = item.messageSeq
	}
	return messageIdMap, nil
}

func (s *Server) Monitor() Monitor {
	return s.defaultMonitor
}

// func (s *Server) proposeChannelClusterConfig(config *ChannelClusterConfig) error {
// 	slotId := s.getChannelSlotId(config.ChannelID)
// 	data, err := config.Marshal()
// 	if err != nil {
// 		return err
// 	}
// 	slot := s.clusterEventListener.clusterconfigManager.slot(slotId)
// 	if slot == nil {
// 		return ErrSlotNotFound
// 	}
// 	if slot.Leader != s.opts.NodeID {
// 		return ErrSlotNotIsLeader
// 	}
// 	return s.ProposeToSlot(slotId, data)
// }

type Monitor interface {
	RequestGoroutine() int64 // 请求协程数
	MessageGoroutine() int64 // 消息处理协程数
	InboundFlightMessageCount() int64
	OutboundFlightMessageCount() int64
	InboundFlightMessageBytes() int64
	OutboundFlightMessageBytes() int64
}

type DefaultMonitor struct {
	s *Server
}

func NewDefaultMonitor(s *Server) *DefaultMonitor {
	return &DefaultMonitor{
		s: s,
	}
}

func (m *DefaultMonitor) RequestGoroutine() int64 {
	return int64(m.s.server.RequestPoolRunning())
}

func (m *DefaultMonitor) MessageGoroutine() int64 {
	return int64(m.s.server.MessagePoolRunning())
}

func (m *DefaultMonitor) InboundFlightMessageCount() int64 {
	return int64(m.s.inbound.Len())
}

func (m *DefaultMonitor) InboundFlightMessageBytes() int64 {
	return int64(m.s.inbound.Size())
}

func (m *DefaultMonitor) OutboundFlightMessageCount() int64 {
	return m.s.nodeManager.outboundFlightMessageCount()
}

func (m *DefaultMonitor) OutboundFlightMessageBytes() int64 {
	return m.s.nodeManager.outboundFlightMessageBytes()
}
