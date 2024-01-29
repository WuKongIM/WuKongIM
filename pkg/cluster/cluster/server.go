package cluster

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

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
	slotManager          *slotManager          // 槽管理 负责管理槽的集群
	nodeManager          *nodeManager          // 节点管理 负责管理节点通信管道
	clusterEventListener *clusterEventListener // 分布式事件监听 用于监听集群事件
	localStorage         *localStorage         // 本地存储 用于存储本地数据
	server               *wkserver.Server      // 分布式通讯服务
	stopper              *syncutil.Stopper
	stopped              bool
	cancelFunc           context.CancelFunc
	cancelCtx            context.Context
	// 其他
	opts *Options
	wklog.Log
}

func New(opts *Options) *Server {

	s := &Server{
		slotManager:          newSlotManager(opts),
		nodeManager:          newNodeManager(),
		clusterEventListener: newClusterEventListener(opts),
		stopper:              syncutil.NewStopper(),
		opts:                 opts,
		Log:                  wklog.NewWKLog(fmt.Sprintf("clusterServer[%d]", opts.NodeID)),
	}

	s.localStorage = newLocalStorage(s)

	s.channelGroupManager = newChannelGroupManager(s)
	addr := s.opts.Addr
	if !strings.HasPrefix(addr, "tcp://") {
		addr = fmt.Sprintf("tcp://%s", addr)
	}
	s.server = wkserver.New(addr)
	opts.Transport = NewDefaultTransport(s)
	if opts.ShardLogStorage == nil {
		opts.ShardLogStorage = NewPebbleShardLogStorage(path.Join(opts.DataDir, "logdb"))
	}
	s.cancelCtx, s.cancelFunc = context.WithCancel(context.Background())
	return s
}

func (s *Server) Start() error {

	err := s.opts.ShardLogStorage.Open()
	if err != nil {
		return err
	}

	// 接受消息
	s.opts.Transport.OnMessage(func(from uint64, m *proto.Message) {
		s.handleMessage(from, m)
	})

	// 开启通讯服务
	s.server.OnMessage(func(conn wknet.Conn, msg *proto.Message) {
		from := s.nodeIdByServerUid(conn.UID())
		s.opts.Transport.RecvMessage(from, msg)
	})
	s.setRoutes()
	err = s.server.Start()
	if err != nil {
		return err
	}
	// 开启本地存储
	err = s.localStorage.open()
	if err != nil {
		return err
	}

	// 开启分布式事件监听
	nodes := s.clusterEventListener.localAllNodes()
	if len(nodes) > 0 {
		s.addNodes(nodes) // 添加通讯节点
	}
	for nodeID, addr := range s.opts.InitNodes {
		s.addNode(nodeID, addr)
	}

	err = s.clusterEventListener.start()
	if err != nil {
		return err
	}

	// 开启槽管理
	err = s.slotManager.start()
	if err != nil {
		return err
	}

	// 开启频道管理
	err = s.channelGroupManager.start()
	if err != nil {
		return err
	}

	s.stopper.RunWorker(s.listenClusterEvent)
	return nil
}

func (s *Server) Stop() {
	s.stopped = true
	s.cancelFunc()
	s.server.Stop()
	s.clusterEventListener.stop()
	s.slotManager.stop()
	s.channelGroupManager.stop()
	err := s.localStorage.close()
	if err != nil {
		s.Error("close localStorage is error", zap.Error(err))
	}
	s.stopper.Stop()

	err = s.opts.ShardLogStorage.Close()
	if err != nil {
		s.Error("close ShardLogStorage is error", zap.Error(err))
	}

}

// 监听分布式事件
func (s *Server) listenClusterEvent() {
	for !s.stopped {
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

	return s.slotManager.proposeAndWaitCommit(slotId, data)
}

func (s *Server) LeaderNodeOfChannel(channelID string, channelType uint8) (nodeInfo clusterconfig.NodeInfo, err error) {
	channel, err := s.channelGroupManager.fetchChannel(channelID, channelType)
	if err != nil {
		return clusterconfig.NodeInfo{}, err
	}
	if channel == nil {
		return clusterconfig.NodeInfo{}, fmt.Errorf("channel[%s] not found", channelID)
	}
	return clusterconfig.NodeInfo{}, nil
}

func (s *Server) SlotIsLeader(slotId uint32) bool {
	st := s.slotManager.slot(slotId)
	if st == nil {
		return false
	}
	return st.isLeader()
}

// 等待leader节点的产生
func (s *Server) WaitNodeLeader(timeout time.Duration) error {
	return s.clusterEventListener.clusterconfigManager.waitNodeLeader(timeout)
}

func (s *Server) handleMessage(from uint64, m *proto.Message) {
	switch m.MsgType {
	case MsgClusterConfigMsg:
		s.handleClusterConfigMsg(from, m)
	case MsgSlotMsg:
		s.handleSlotMsg(from, m)
	case MsgChannelMsg:
		s.handleChannelMsg(from, m)
	}
}

func (s *Server) WaitConfigSlotCount(count uint32, timeout time.Duration) error {
	return s.clusterEventListener.waitConfigSlotCount(count, timeout)
}

func (s *Server) MustWaitConfigSlotCount(count uint32, timeout time.Duration) {
	err := s.WaitConfigSlotCount(count, timeout)
	if err != nil {
		panic(err)
	}
}

func (s *Server) Options() *Options {
	return s.opts
}

func (s *Server) handleClusterConfigMsg(from uint64, m *proto.Message) {
	cfgMsg := &clusterconfig.Message{}
	err := cfgMsg.Unmarshal(m.Content)
	if err != nil {
		s.Error("unmarshal cluster config message error", zap.Error(err))
		return
	}
	s.clusterEventListener.handleMessage(*cfgMsg)
}

func (s *Server) handleSlotMsg(from uint64, m *proto.Message) {

	msg, err := UnmarshalMessage(m.Content)
	if err != nil {
		s.Error("unmarshal slot message error", zap.Error(err))
		return
	}
	slotId := GetSlotId(msg.ShardNo, s.Log)
	s.slotManager.handleMessage(slotId, msg.Message)
}

func (s *Server) handleChannelMsg(from uint64, m *proto.Message) {
	msg, err := UnmarshalMessage(m.Content)
	if err != nil {
		s.Error("unmarshal slot message error", zap.Error(err))
		return
	}
	channelID, channelType := ChannelFromChannelKey(msg.ShardNo)
	err = s.channelGroupManager.handleMessage(channelID, channelType, msg.Message)
	if err != nil {
		s.Error("handle channel message error", zap.Error(err))
		return
	}
}
