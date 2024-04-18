package cluster

import (
	"context"
	"fmt"
	"path"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/clusterconfig/pb"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/clusterevent"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/icluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/keylock"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"github.com/bwmarrin/snowflake"
	"github.com/panjf2000/ants/v2"
	"go.uber.org/zap"
)

var _ icluster.Cluster = (*Server)(nil)

type Server struct {
	opts               *Options
	clusterEventServer *clusterevent.Server // 分布式配置事件中心
	nodeManager        *nodeManager         // 节点管理者
	slotManager        *slotManager         // 槽管理者
	channelManager     *channelManager      // 频道管理者

	channelKeyLock         *keylock.KeyLock        // 频道锁
	netServer              *wkserver.Server        // 节点之间通讯的网络服务
	channelElectionPool    *ants.Pool              // 频道选举的协程池
	channelElectionManager *channelElectionManager // 频道选举管理者
	channelLoadPool        *ants.Pool              // 加载频道的协程池
	channelLoadMap         map[string]struct{}     // 频道是否在加载中的map
	channelLoadMapLock     sync.RWMutex            // 频道是否在加载中的map锁
	stopC                  chan struct{}
	cancelCtx              context.Context
	cancelFnc              context.CancelFunc
	onMessageFnc           func(msg *proto.Message) // 上层处理消息的函数
	logIdGen               *snowflake.Node          // 日志id生成
	slotStorage            *PebbleShardLogStorage
	apiPrefix              string    // api前缀
	uptime                 time.Time // 服务器启动时间
	wklog.Log
}

func New(opts *Options) *Server {

	s := &Server{
		opts:           opts,
		nodeManager:    newNodeManager(opts),
		slotManager:    newSlotManager(opts),
		channelManager: newChannelManager(opts),
		Log:            wklog.NewWKLog(fmt.Sprintf("cluster[%d]", opts.NodeId)),
		channelKeyLock: keylock.NewKeyLock(),
		stopC:          make(chan struct{}),
		channelLoadMap: make(map[string]struct{}),
	}

	if opts.SlotLogStorage == nil {
		s.slotStorage = NewPebbleShardLogStorage(path.Join(opts.DataDir, "logdb"))
		opts.SlotLogStorage = s.slotStorage
	}

	logIdGen, err := snowflake.NewNode(int64(opts.NodeId))
	if err != nil {
		s.Panic("new logIdGen failed", zap.Error(err))
	}
	s.logIdGen = logIdGen

	cfgDir := path.Join(opts.DataDir, "config")

	opts.Send = s.send
	s.clusterEventServer = clusterevent.New(clusterevent.NewOptions(
		clusterevent.WithNodeId(opts.NodeId),
		clusterevent.WithInitNodes(opts.InitNodes),
		clusterevent.WithSlotCount(opts.SlotCount),
		clusterevent.WithSlotMaxReplicaCount(opts.SlotMaxReplicaCount),
		clusterevent.WithReady(s.onEvent),
		clusterevent.WithSend(s.onSend),
		clusterevent.WithConfigDir(cfgDir),
	))

	channelElectionPool, err := ants.NewPool(s.opts.ChannelElectionPoolSize, ants.WithNonblocking(false), ants.WithDisablePurge(true), ants.WithPanicHandler(func(err interface{}) {
		stack := debug.Stack()
		s.Panic("频道选举协程池崩溃", zap.Error(err.(error)), zap.String("stack", string(stack)))
	}))
	if err != nil {
		s.Panic("new channelElectionPool failed", zap.Error(err))
	}
	s.channelElectionPool = channelElectionPool

	s.channelLoadPool, err = ants.NewPool(s.opts.ChannelLoadPoolSize, ants.WithNonblocking(true), ants.WithPanicHandler(func(err interface{}) {
		stack := debug.Stack()
		s.Panic("频道加载协程池崩溃", zap.Error(err.(error)), zap.String("stack", string(stack)))
	}))
	if err != nil {
		s.Panic("new channelLoadPool failed", zap.Error(err))
	}

	s.netServer = wkserver.New(opts.Addr, wkserver.WithMessagePoolOn(false))
	s.channelElectionManager = newChannelElectionManager(s)
	s.cancelCtx, s.cancelFnc = context.WithCancel(context.Background())
	return s
}

func (s *Server) Start() error {

	s.uptime = time.Now()

	err := s.slotStorage.Open()
	if err != nil {
		return err
	}

	s.channelKeyLock.StartCleanLoop()

	if len(s.opts.InitNodes) > 0 {
		for nodeId, clusterAddr := range s.opts.InitNodes {
			if nodeId == s.opts.NodeId {
				continue
			}
			s.nodeManager.addNode(s.newNodeByNodeInfo(nodeId, clusterAddr))
		}
	}

	// channel election manager
	err = s.channelElectionManager.start()
	if err != nil {
		return err
	}

	// net server
	s.setRoutes()
	s.netServer.OnMessage(s.onMessage)
	err = s.netServer.Start()
	if err != nil {
		return err
	}
	// slot manager
	err = s.slotManager.start()
	if err != nil {
		return err
	}
	// channel manager
	err = s.channelManager.start()
	if err != nil {
		return err
	}
	// cluster event server
	err = s.clusterEventServer.Start()
	if err != nil {
		return err
	}
	return nil
}

func (s *Server) Stop() {
	close(s.stopC)
	s.cancelFnc()
	fmt.Println("node stopped1")
	s.nodeManager.stop()
	fmt.Println("node stopped2")
	s.channelElectionManager.stop()
	fmt.Println("node stopped3")
	s.netServer.Stop()
	fmt.Println("node stopped4")
	s.clusterEventServer.Stop()
	fmt.Println("node stopped5")
	s.slotManager.stop()
	fmt.Println("node stopped6")
	s.channelManager.stop()
	fmt.Println("node stopped7")
	s.channelKeyLock.StopCleanLoop()
	fmt.Println("node stopped8")
	s.slotStorage.Close()

	s.Debug("Server stopped")
}

func (s *Server) AddSlotMessage(m reactor.Message) {
	s.slotManager.addMessage(m)
}

func (s *Server) AddConfigMessage(m reactor.Message) {
	s.clusterEventServer.AddMessage(m)
}

func (s *Server) AddChannelMessage(m reactor.Message) {

	ch := s.channelManager.getWithHandleKey(m.HandlerKey)
	if ch != nil {
		s.channelManager.addMessage(m)
		return
	}

	s.channelLoadMapLock.RLock()
	if _, ok := s.channelLoadMap[m.HandlerKey]; ok {
		s.channelLoadMapLock.RUnlock()
		return
	}
	s.channelLoadMapLock.RUnlock()

	s.channelLoadMapLock.Lock()
	s.channelLoadMap[m.HandlerKey] = struct{}{}
	s.channelLoadMapLock.Unlock()

	err := s.channelLoadPool.Submit(func() {
		channelId, channelType := ChannelFromlKey(m.HandlerKey)
		if channelId == "" {
			s.Panic("channelId is empty", zap.String("handlerKey", m.HandlerKey))
		}
		_, err := s.loadOrCreateChannel(s.cancelCtx, channelId, channelType)
		if err != nil {
			s.Error("loadOrCreateChannel failed", zap.Error(err), zap.String("handlerKey", m.HandlerKey), zap.String("msgType", m.MsgType.String()))
		}
		s.channelLoadMapLock.Lock()
		delete(s.channelLoadMap, m.HandlerKey)
		s.channelLoadMapLock.Unlock()
	})
	if err != nil {
		s.Error("channelLoadPool.Submit failed", zap.Error(err))
	}
}

func (s *Server) onEvent(msgs []clusterevent.Message) {
	s.Debug("收到分布式事件....")
	handled := false
	for _, msg := range msgs {
		handled = s.handleClusterEvent(msg)
		if handled {
			s.clusterEventServer.Step(msg)
		}
	}
}

func (s *Server) handleClusterEvent(m clusterevent.Message) bool {

	switch m.Type {
	case clusterevent.EventTypeNodeAdd: // 添加节点
		s.handleNodeAdd(m)
	case clusterevent.EventTypeNodeUpdate: // 修改节点
		s.handleNodeUpdate(m)
	case clusterevent.EventTypeSlotAdd: // 添加槽
		s.handleSlotAdd(m)
	}
	return true

}

func (s *Server) handleNodeAdd(m clusterevent.Message) {
	for _, node := range m.Nodes {
		if s.nodeManager.exist(node.Id) {
			continue
		}
		if s.opts.NodeId == node.Id {
			continue
		}
		if strings.TrimSpace(node.ClusterAddr) != "" {
			s.nodeManager.addNode(s.newNodeByNodeInfo(node.Id, node.ClusterAddr))
		}
	}
}

func (s *Server) handleNodeUpdate(m clusterevent.Message) {
	for _, node := range m.Nodes {
		if node.Id == s.opts.NodeId {
			continue
		}
		if !s.nodeManager.exist(node.Id) && strings.TrimSpace(node.ClusterAddr) != "" {
			s.nodeManager.addNode(s.newNodeByNodeInfo(node.Id, node.ClusterAddr))
			continue
		}
		n := s.nodeManager.node(node.Id)
		if n != nil && n.addr != node.ClusterAddr {
			s.nodeManager.removeNode(n.id)
			n.stop()
			s.nodeManager.addNode(s.newNodeByNodeInfo(node.Id, node.ClusterAddr))
			continue
		}

	}
}

func (s *Server) handleSlotAdd(m clusterevent.Message) {
	for _, slot := range m.Slots {
		if s.slotManager.exist(slot.Id) {
			continue
		}
		if !wkutil.ArrayContainsUint64(slot.Replicas, s.opts.NodeId) {
			continue
		}
		st := s.newSlot(slot)
		s.slotManager.add(st)

		if slot.Leader != 0 {
			if slot.Leader == s.opts.NodeId {
				st.becomeLeader(slot.Term)
			} else {
				st.becomeFollower(slot.Term, slot.Leader)
			}
		}

	}
}

func (s *Server) newNodeByNodeInfo(nodeID uint64, addr string) *node {
	n := newNode(nodeID, s.serverUid(s.opts.NodeId), addr, s.opts)
	n.start()
	return n
}

func (s *Server) newSlot(st *pb.Slot) *slot {

	slot := newSlot(st, s.opts)
	return slot

}

func (s *Server) serverUid(id uint64) string {
	return fmt.Sprintf("%d", id)
}

func (s *Server) send(shardType ShardType, m reactor.Message) {
	node := s.nodeManager.node(m.To)
	if node == nil {
		s.Warn("send failed, node not exist", zap.Uint64("to", m.To))
		return
	}
	data, err := m.Marshal()
	if err != nil {
		s.Error("Marshal failed", zap.Error(err))
		return
	}

	var msgType uint32
	if shardType == ShardTypeSlot {
		msgType = MsgTypeSlot
	} else if shardType == ShardTypeConfig {
		msgType = MsgTypeConfig
	} else if shardType == ShardTypeChannel {
		msgType = MsgTypeChannel
	} else {
		s.Error("send failed, invalid shardType", zap.Uint8("shardType", uint8(shardType)))
		return
	}
	err = node.send(&proto.Message{
		MsgType: msgType,
		Content: data,
	})
	if err != nil {
		s.Error("send failed", zap.Error(err))
		return
	}
}

func (s *Server) onSend(m reactor.Message) {

	s.opts.Send(ShardTypeConfig, m)
}

func (s *Server) onMessage(c wknet.Conn, m *proto.Message) {

	switch m.MsgType {
	case MsgTypeConfig:
		msg, err := reactor.UnmarshalMessage(m.Content)
		if err != nil {
			s.Error("UnmarshalMessage failed", zap.Error(err))
			return
		}
		s.AddConfigMessage(msg)
	case MsgTypeSlot:
		msg, err := reactor.UnmarshalMessage(m.Content)
		if err != nil {
			s.Error("UnmarshalMessage failed", zap.Error(err))
			return
		}
		s.AddSlotMessage(msg)
	case MsgTypeChannel:
		msg, err := reactor.UnmarshalMessage(m.Content)
		if err != nil {
			s.Error("UnmarshalMessage failed", zap.Error(err))
			return
		}
		s.AddChannelMessage(msg)
	default:
		if s.onMessageFnc != nil {
			s.onMessageFnc(m)
		}
	}
}

// 获取频道所在的slotId
func (s *Server) getChannelSlotId(channelId string) uint32 {
	var slotCount uint32 = s.clusterEventServer.SlotCount()
	if slotCount == 0 {
		slotCount = s.opts.SlotCount
	}
	return wkutil.GetSlotNum(int(slotCount), channelId)
}
