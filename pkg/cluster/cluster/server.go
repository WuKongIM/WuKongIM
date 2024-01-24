package cluster

import (
	"context"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/cluster/clusterconfig"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/lni/goutils/syncutil"
	"go.uber.org/zap"
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
	channelGroupManager  *channelGroupManager  // 频道管理，负责管理频道的集群
	slotGroupManager     *slotGroupManager     // 槽管理 负责管理槽的集群
	nodeGroupManager     *nodeGroupManager     // 节点管理 负责管理节点通信管道
	clusterEventListener *clusterEventListener // 分布式事件监听 用于监听集群事件
	localStorage         *localStorage         // 本地存储 用于存储本地数据
	stopper              *syncutil.Stopper

	// 其他
	opts *Options
	wklog.Log
}

func New(opts *Options) *Server {
	s := &Server{
		channelGroupManager:  newChannelGroupManager(opts),
		slotGroupManager:     newSlotGroupManager(opts),
		nodeGroupManager:     newNodeGroupManager(),
		clusterEventListener: newClusterEventListener(opts),
		stopper:              syncutil.NewStopper(),
		localStorage:         newLocalStorage(opts),
		opts:                 opts,
	}
	return s
}

func (s *Server) Start() error {
	nodes := s.clusterEventListener.localAllNodes()
	if len(nodes) > 0 {
		s.addNodes(nodes) // 添加通讯节点
	}

	err := s.localStorage.open()
	if err != nil {
		return err
	}

	err = s.slotGroupManager.start()
	if err != nil {
		return err
	}
	s.stopper.RunWorker(s.listenClusterEvent)
	return nil
}

func (s *Server) Stop() {
	s.stopper.Stop()
	s.slotGroupManager.stop()
	err := s.localStorage.close()
	if err != nil {
		s.Error("close localStorage is error", zap.Error(err))
	}
}

// 监听分布式事件
func (s *Server) listenClusterEvent() {
	for {
		event := s.clusterEventListener.wait()
		if !IsEmptyClusterEvent(event) {
			s.handleClusterEvent(event)
		}
	}
}

// ProposeMessageToChannel 提案消息到指定的频道
func (s *Server) ProposeMessageToChannel(channelID string, channelType uint8, data []byte) (uint64, error) {

	return s.channelGroupManager.proposeMessage(channelID, channelType, data)
}

// ProposeToSlot 提交数据到指定的槽
func (s *Server) ProposeToSlot(slotId uint32, data []byte) error {

	return s.slotGroupManager.proposeAndWaitCommit(slotId, data)
}

func (s *Server) LeaderNodeOfChannel(channelID string, channelType uint8) (nodeInfo clusterconfig.NodeInfo, err error) {
	channel, err := s.channelGroupManager.channel(channelID, channelType)
	if err != nil {
		return clusterconfig.NodeInfo{}, err
	}
	if channel == nil {
		return clusterconfig.NodeInfo{}, fmt.Errorf("channel[%s] not found", channelID)
	}
	return clusterconfig.NodeInfo{}, nil
}
