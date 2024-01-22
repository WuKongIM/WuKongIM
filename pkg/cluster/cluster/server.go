package cluster

import (
	"context"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/cluster/clusterconfig"
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/lni/goutils/syncutil"
)

type ICluster interface {
	Start() error
	Stop()

	// LeaderNodeIDOfChannel 获取channel的leader节点ID
	LeaderNodeIDOfChannel(channelID string, channelType uint8) (nodeID uint64, err error)

	// LeaderNodeOfChannel 获取channel的leader节点信息
	LeaderNodeOfChannel(channelID string, channelType uint8) (nodeInfo clusterconfig.NodeInfo, err error)

	// IsLeaderNodeOfChannel 当前节点是否是channel的leader节点
	IsLeaderNodeOfChannel(channelID string, channelType uint8) (isLeader bool, err error)
	// NodeInfoByID 获取节点信息
	NodeInfoByID(nodeID uint64) (nodeInfo clusterconfig.NodeInfo, err error)
	//Route 设置接受请求的路由
	Route(path string, handler wkserver.Handler)
	// RequestWithContext 发送请求给指定的节点
	RequestWithContext(ctx context.Context, toNodeID uint64, path string, body []byte) (*proto.Response, error)
	// Send 发送消息给指定的节点, MsgType 使用 1000 - 2000之间的值
	Send(toNodeID uint64, msg *proto.Message) error
	// OnMessage 设置接收消息的回调
	OnMessage(f func(conn wknet.Conn, msg *proto.Message))
	// 节点是否在线
	NodeIsOnline(nodeID uint64) bool
}

type Server struct {
	channelGroupManager  *ChannelGroupManager  // 频道管理
	slotGroupManager     *SlotGroupManager     // 槽管理
	nodeGroupManager     *NodeGroupManager     // 节点管理
	clusterEventListener *ClusterEventListener // 分布式事件监听
	localStorage         *LocalStorage         // 本地存储
	stopper              *syncutil.Stopper
}

func (s *Server) Start() error {
	s.stopper.RunWorker(s.listenClusterEvent)
	return nil
}

func (s *Server) Stop() error {
	s.stopper.Stop()
	return nil
}

func (s *Server) listenClusterEvent() {
	for {
		event := s.clusterEventListener.Wait()
		fmt.Println(event)
		s.handleClusterEvent(event)
	}
}

func (s *Server) ProposeMessageToChannel(channelID string, channelType uint8, data []byte) (uint64, error) {

	return s.channelGroupManager.ProposeMessage(channelID, channelType, data)
}

func (s *Server) LeaderNodeOfChannel(channelID string, channelType uint8) (nodeInfo clusterconfig.NodeInfo, err error) {
	channel, err := s.channelGroupManager.Channel(channelID, channelType)
	if err != nil {
		return clusterconfig.NodeInfo{}, err
	}
	if channel == nil {
		return clusterconfig.NodeInfo{}, fmt.Errorf("channel[%s] not found", channelID)
	}
	return clusterconfig.NodeInfo{}, nil
}
