package cluster

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/node/types"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
)

// GetOrCreateChannelClusterConfig 获取或创建频道分布式配置
func (s *Server) GetOrCreateChannelClusterConfigFromSlotLeader(channelId string, channelType uint8) (wkdb.ChannelClusterConfig, error) {

	s.channelKeyLock.Lock(channelId)
	defer s.channelKeyLock.Unlock(channelId)

	// 获取频道槽领导
	slotLeaderId, err := s.SlotLeaderIdOfChannel(channelId, channelType)
	if err != nil {
		return wkdb.EmptyChannelClusterConfig, err
	}

	// 如果当前节点是频道槽领导，则直接返回频道分布式配置
	if s.opts.ConfigOptions.NodeId == slotLeaderId {
		return s.getOrCreateChannelClusterConfigFromLocal(channelId, channelType)
	}

	// 向频道槽领导请求频道分布式配置
	return s.rpcClient.RequestGetOrCreateChannelClusterConfig(slotLeaderId, channelId, channelType)
}

func (s *Server) SlotLeaderOfChannel(channelId string, channelType uint8) (*types.Node, error) {
	slotId := s.getSlotId(channelId)
	slotLeaderId := s.cfgServer.SlotLeaderId(slotId)
	if slotLeaderId == 0 {
		return nil, fmt.Errorf("slot[%d] leader not found", slotId)
	}
	node := s.cfgServer.Node(slotLeaderId)
	if node == nil {
		return nil, errors.New("slot leader node not found")
	}
	return node, nil
}

func (s *Server) SlotLeaderIdOfChannel(channelId string, channelType uint8) (nodeID uint64, err error) {
	slotId := s.getSlotId(channelId)
	slotLeaderId := s.cfgServer.SlotLeaderId(slotId)
	if slotLeaderId == 0 {
		return 0, fmt.Errorf("slot[%d] leader not found", slotId)
	}
	return slotLeaderId, nil
}

func (s *Server) LoadOnlyChannelClusterConfig(channelId string, channelType uint8) (wkdb.ChannelClusterConfig, error) {
	slotId := s.getSlotId(channelId)
	slotLeaderId := s.cfgServer.SlotLeaderId(slotId)
	if slotLeaderId == 0 {
		return wkdb.EmptyChannelClusterConfig, fmt.Errorf("slot[%d] leader not found", slotId)
	}
	if s.opts.ConfigOptions.NodeId == slotLeaderId {
		return s.db.GetChannelClusterConfig(channelId, channelType)
	}

	return s.rpcClient.RequestGetChannelClusterConfig(slotLeaderId, channelId, channelType)
}

func (s *Server) LeaderOfChannel(channelId string, channelType uint8) (*types.Node, error) {
	clusterConfig, err := s.GetOrCreateChannelClusterConfigFromSlotLeader(channelId, channelType)
	if err != nil {
		return nil, err
	}
	node := s.cfgServer.Node(clusterConfig.LeaderId)
	if node == nil {
		return nil, ErrChannelClusterConfigNotFound
	}
	return node, nil
}

func (s *Server) LeaderIdOfChannel(channelId string, channelType uint8) (uint64, error) {
	clusterConfig, err := s.GetOrCreateChannelClusterConfigFromSlotLeader(channelId, channelType)
	if err != nil {
		return 0, err
	}
	return clusterConfig.LeaderId, nil
}

func (s *Server) LeaderOfChannelForRead(channelId string, channelType uint8) (*types.Node, error) {
	clusterConfig, err := s.LoadOnlyChannelClusterConfig(channelId, channelType)
	if err != nil {
		return nil, err
	}
	node := s.cfgServer.Node(clusterConfig.LeaderId)
	if node == nil {
		return nil, ErrChannelClusterConfigNotFound
	}
	return node, nil
}

func (s *Server) NodeInfoById(nodeId uint64) *types.Node {
	return s.cfgServer.Node(nodeId)
}

func (s *Server) GetSlotId(v string) uint32 {
	return s.getSlotId(v)
}

func (s *Server) SlotLeaderId(slotId uint32) uint64 {
	return s.cfgServer.SlotLeaderId(slotId)
}

func (s *Server) NodeIsOnline(nodeId uint64) bool {
	return s.cfgServer.NodeIsOnline(nodeId)
}

func (s *Server) SlotLeaderNodeInfo(slotId uint32) *types.Node {
	slotLeaderId := s.slotServer.SlotLeaderId(slotId)
	return s.cfgServer.Node(slotLeaderId)
}

func (s *Server) NodeVersion() uint64 {
	return s.cfgServer.GetClusterConfig().Version
}

func (s *Server) Nodes() []*types.Node {
	return s.cfgServer.Nodes()
}

func (s *Server) Send(nodeId uint64, msg *proto.Message) error {
	node := s.nodeManager.node(nodeId)
	if node == nil {
		return ErrNodeNotFound
	}
	return node.send(msg)
}

func (s *Server) RequestWithContext(ctx context.Context, toNodeId uint64, path string, body []byte) (*proto.Response, error) {
	node := s.nodeManager.node(toNodeId)
	if node == nil {
		return nil, ErrNodeNotFound
	}
	return node.requestWithContext(ctx, path, body)
}

func (s *Server) Route(path string, handler wkserver.Handler) {
	s.netServer.Route(path, handler)
}

func (s *Server) LeaderId() uint64 {
	return s.cfgServer.LeaderId()
}

// OnMessage 注册消息处理函数
func (s *Server) OnMessage(f func(fromNodeId uint64, msg *proto.Message)) {
	s.onMessageFnc = f
}

func (s *Server) MustWaitAllSlotsReady(timeout time.Duration) {
	s.slotServer.MustWaitAllSlotsReady(timeout)
}

func (s *Server) MustWaitClusterReady(timeout time.Duration) error {
	return nil
}
