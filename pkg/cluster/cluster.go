package cluster

import (
	"context"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/wkserver"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
)

type ICluster interface {
	Start() error
	Stop()

	// LeaderNodeIDOfChannel 获取channel的leader节点ID
	LeaderNodeIDOfChannel(channelID string, channelType uint8) (nodeID uint64, err error)

	// LeaderNodeOfChannel 获取channel的leader节点信息
	LeaderNodeOfChannel(channelID string, channelType uint8) (nodeInfo NodeInfo, err error)

	// IsLeaderNodeOfChannel 当前节点是否是channel的leader节点
	IsLeaderNodeOfChannel(channelID string, channelType uint8) (isLeader bool, err error)
	// NodeInfoByID 获取节点信息
	NodeInfoByID(nodeID uint64) (nodeInfo *NodeInfo, err error)
	// RequestWithContext 发送请求给指定的节点
	RequestWithContext(ctx context.Context, toNodeID uint64, path string, body []byte) (*proto.Response, error)
	// 设置路由
	Route(path string, handler wkserver.Handler)
}

type NodeInfo struct {
	NodeID            uint64 // 节点ID
	ClusterServerAddr string // 集群服务地址
	ApiServerAddr     string // API服务地址
}

func (s *Server) LeaderNodeIDOfChannel(channelID string, channelType uint8) (nodeID uint64, err error) {
	channel, err := s.channelManager.GetChannel(channelID, channelType)
	if err != nil {
		return 0, err
	}
	if channel == nil {
		return 0, fmt.Errorf("channel[%s] not found", channelID)
	}
	return channel.LeaderID(), nil
}

func (s *Server) LeaderNodeOfChannel(channelID string, channelType uint8) (nodeInfo NodeInfo, err error) {
	channel, err := s.channelManager.GetChannel(channelID, channelType)
	if err != nil {
		return NodeInfo{}, err
	}
	if channel == nil {
		return NodeInfo{}, fmt.Errorf("channel[%s] not found", channelID)
	}
	node := s.clusterEventManager.GetNode(channel.LeaderID())
	return NodeInfo{
		NodeID:            node.Id,
		ClusterServerAddr: node.ClusterAddr,
		ApiServerAddr:     node.ApiAddr,
	}, nil
}

func (s *Server) IsLeaderNodeOfChannel(channelID string, channelType uint8) (bool, error) {
	channel, err := s.channelManager.GetChannel(channelID, channelType)
	if err != nil {
		return false, err
	}
	if channel == nil {
		return false, fmt.Errorf("channel[%s] not found", channelID)
	}
	return channel.IsLeader(), nil
}

func (s *Server) NodeInfoByID(nodeID uint64) (nodeInfo *NodeInfo, err error) {
	node := s.clusterEventManager.GetNode(nodeID)
	if node == nil {
		return nil, fmt.Errorf("node[%d] not found", nodeID)
	}
	return &NodeInfo{
		NodeID:            node.Id,
		ClusterServerAddr: node.ClusterAddr,
		ApiServerAddr:     node.ApiAddr,
	}, nil
}

func (s *Server) RequestWithContext(ctx context.Context, toNodeID uint64, path string, body []byte) (*proto.Response, error) {
	node := s.nodeManager.getNode(toNodeID)
	if node == nil {
		return nil, fmt.Errorf("node[%d] not found", toNodeID)
	}
	return node.RequestWithContext(ctx, path, body)
}

func (s *Server) Route(path string, handler wkserver.Handler) {
	s.clusterServer.Route(path, handler)
}
