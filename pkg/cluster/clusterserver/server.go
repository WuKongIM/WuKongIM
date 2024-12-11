package cluster

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/clusterconfig/pb"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/clusterevent"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/icluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/keylock"
	"github.com/WuKongIM/WuKongIM/pkg/trace"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"github.com/bwmarrin/snowflake"
	"github.com/lni/goutils/syncutil"
	"github.com/panjf2000/ants/v2"
	"github.com/panjf2000/gnet/v2"
	"go.uber.org/atomic"
	"go.uber.org/zap"
	"k8s.io/utils/lru"
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
	cancelCtx              context.Context
	cancelFnc              context.CancelFunc
	onMessageFnc           func(fromNodeId uint64, msg *proto.Message) // 上层处理消息的函数
	logIdGen               *snowflake.Node                             // 日志id生成
	slotStorage            *PebbleShardLogStorage                      // slot存储
	apiPrefix              string                                      // api前缀
	uptime                 time.Time                                   // 服务器启动时间
	wklog.Log
	stopped atomic.Bool
	stopper *syncutil.Stopper

	channelClusterCache *lru.Cache // 缓存最热的ChannelClusterConfig
	// 缓存的数据版本，这个版本是全局分布式配置版本，当全局分布式配置的版本大于当前时，应当清除缓存
	channelClusterCacheVersion uint64

	// 测试ping
	testPings       map[string]*ping
	testPingMapLock sync.RWMutex
}

func New(opts *Options) *Server {

	s := &Server{
		opts:                opts,
		nodeManager:         newNodeManager(opts),
		Log:                 wklog.NewWKLog(fmt.Sprintf("cluster[%d]", opts.NodeId)),
		channelKeyLock:      keylock.NewKeyLock(), // 创建指定大小的锁环
		stopper:             syncutil.NewStopper(),
		channelClusterCache: lru.New(1024),
		testPings:           make(map[string]*ping),
	}

	s.slotManager = newSlotManager(s)
	s.channelManager = newChannelManager(s)

	if opts.SlotLogStorage == nil {
		s.slotStorage = NewPebbleShardLogStorage(path.Join(opts.DataDir, "logdb"), uint32(opts.SlotDbShardNum))
		opts.SlotLogStorage = s.slotStorage
	}

	logIdGen, err := snowflake.NewNode(int64(opts.NodeId))
	if err != nil {
		s.Panic("new logIdGen failed", zap.Error(err))
	}
	s.logIdGen = logIdGen

	cfgDir := path.Join(opts.DataDir, "config")

	initNodes := opts.InitNodes
	if len(initNodes) == 0 {
		if strings.TrimSpace(s.opts.Seed) != "" {
			seedNodeID, seedAddr, err := seedNode(s.opts.Seed)
			if err != nil {
				panic(err)
			}
			initNodes[seedNodeID] = seedAddr
		}
		if s.opts.ServerAddr != "" {
			initNodes[s.opts.NodeId] = strings.ReplaceAll(s.opts.ServerAddr, "tcp://", "")
		} else {
			initNodes[s.opts.NodeId] = strings.ReplaceAll(s.opts.Addr, "tcp://", "")
		}

	}

	opts.Send = s.send
	s.clusterEventServer = clusterevent.New(clusterevent.NewOptions(
		clusterevent.WithNodeId(opts.NodeId),
		clusterevent.WithInitNodes(initNodes),
		clusterevent.WithSeed(s.opts.Seed),
		clusterevent.WithSlotCount(opts.SlotCount),
		clusterevent.WithSlotMaxReplicaCount(opts.SlotMaxReplicaCount),
		clusterevent.WithChannelMaxReplicaCount(uint32(opts.ChannelMaxReplicaCount)),
		clusterevent.WithOnClusterConfigChange(s.onClusterConfigChange),
		clusterevent.WithOnSlotElection(s.onSlotElection),
		clusterevent.WithSend(s.onSend),
		clusterevent.WithConfigDir(cfgDir),
		clusterevent.WithApiServerAddr(opts.ApiServerAddr),
		clusterevent.WithCluster(s),
		clusterevent.WithElectionIntervalTick(opts.ElectionIntervalTick),
		clusterevent.WithHeartbeatIntervalTick(opts.HeartbeatIntervalTick),
		clusterevent.WithTickInterval(opts.TickInterval),
		clusterevent.WithPongMaxTick(opts.PongMaxTick),
	))

	channelElectionPool, err := ants.NewPool(s.opts.ChannelElectionPoolSize, ants.WithNonblocking(false), ants.WithDisablePurge(true), ants.WithPanicHandler(func(err interface{}) {
		s.Panic("频道选举协程池崩溃", zap.Any("err", err), zap.Stack("stack"))
	}))
	if err != nil {
		s.Panic("new channelElectionPool failed", zap.Error(err))
	}
	s.channelElectionPool = channelElectionPool

	s.netServer = wkserver.New(
		opts.Addr, wkserver.WithMessagePoolOn(false),
		wkserver.WithOnRequest(func(conn gnet.Conn, req *proto.Request) {
			trace.GlobalTrace.Metrics.System().IntranetIncomingAdd(int64(len(req.Body)))
		}),
		wkserver.WithOnResponse(func(conn gnet.Conn, resp *proto.Response) {
			trace.GlobalTrace.Metrics.System().IntranetOutgoingAdd(int64(len(resp.Body)))
		}),
	)
	s.channelElectionManager = newChannelElectionManager(s)
	s.cancelCtx, s.cancelFnc = context.WithCancel(context.Background())
	return s
}

func (s *Server) Start() error {

	s.uptime = time.Now()

	s.channelKeyLock.StartCleanLoop()

	err := s.slotStorage.Open()
	if err != nil {
		return err
	}

	nodes := s.clusterEventServer.Nodes()
	if len(nodes) > 0 {
		for _, node := range nodes {
			if node.Id == s.opts.NodeId {
				continue
			}
			s.nodeManager.addNode(s.newNodeByNodeInfo(node.Id, node.ClusterAddr))
		}
	} else if len(s.opts.InitNodes) > 0 {
		for nodeId, clusterAddr := range s.opts.InitNodes {
			if nodeId == s.opts.NodeId {
				continue
			}
			s.nodeManager.addNode(s.newNodeByNodeInfo(nodeId, clusterAddr))
		}
	}

	// 分布式事件开启
	slots := s.clusterEventServer.Slots()
	for _, slot := range slots {
		if !wkutil.ArrayContainsUint64(slot.Replicas, s.opts.NodeId) {
			continue
		}
		s.addSlot(slot)
	}

	// channel election manager
	err = s.channelElectionManager.start()
	if err != nil {
		return err
	}

	// cluster event server
	err = s.clusterEventServer.Start()
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

	// 如果有新加入的节点 则执行加入逻辑
	if s.needJoin() { // 需要加入集群
		// s.clusterEventServer.SetIsPrepared(false) // 先将节点集群准备状态设置为false，等待加入集群后再设置为true
		s.stopper.RunWorker(s.joinLoop)
	}

	// 设置监控数据的observer
	s.setObservers()

	return nil
}

func (s *Server) setObservers() {
	// 收集节点请求中的数据
	trace.GlobalTrace.Metrics.Cluster().ObserverNodeRequesting(func() int64 {

		return s.nodeManager.requesting()
	})

	// 收集消息发送中的数量
	trace.GlobalTrace.Metrics.Cluster().ObserverNodeSending(func() int64 {

		return s.nodeManager.sending()
	})
}

func (s *Server) Stop() {

	s.stopped.Store(true)
	s.cancelFnc()
	s.stopper.Stop()
	s.nodeManager.stop()
	s.channelElectionManager.stop()
	s.netServer.Stop()
	s.clusterEventServer.Stop()
	s.slotManager.stop()
	s.channelManager.stop()
	s.slotStorage.Close()

	s.channelKeyLock.StopCleanLoop()

}

// 提案频道分布式配置
func (s *Server) ProposeChannelClusterConfig(cfg wkdb.ChannelClusterConfig) error {
	return s.opts.ChannelClusterStorage.Propose(cfg)
}

// 获取分布式配置
func (s *Server) GetConfig() *pb.Config {
	return s.clusterEventServer.Config()
}

// 迁移槽
func (s *Server) MigrateSlot(slotId uint32, fromNodeId, toNodeId uint64) error {

	return s.clusterEventServer.ProposeMigrateSlot(slotId, fromNodeId, toNodeId)
}

func (s *Server) AddSlotMessage(m reactor.Message) {

	// 统计引入的消息
	traceIncomingMessage(trace.ClusterKindSlot, m.MsgType, int64(m.Size()))

	s.slotManager.addMessage(m)
}

func (s *Server) AddConfigMessage(m reactor.Message) {

	s.clusterEventServer.AddMessage(m)
}

func (s *Server) AddChannelMessage(m reactor.Message) {

	// 统计引入的消息
	traceIncomingMessage(trace.ClusterKindChannel, m.MsgType, int64(m.Size()))

	// 获取或创建频道处理者
	_ = s.channelManager.getOrCreateIfNotExistWithHandleKey(m.HandlerKey)

	s.channelManager.addMessage(m)

	// s.channelLoadMapLock.RLock()
	// if _, ok := s.channelLoadMap[m.HandlerKey]; ok {
	// 	s.channelLoadMapLock.RUnlock()
	// 	return
	// }
	// s.channelLoadMapLock.RUnlock()

	// s.channelLoadMapLock.Lock()
	// s.channelLoadMap[m.HandlerKey] = struct{}{}
	// s.channelLoadMapLock.Unlock()

	// running := s.channelLoadPool.Running()
	// if running > s.opts.ChannelLoadPoolSize-10 {
	// 	s.Warn("channelLoadPool is busy", zap.Int("running", running), zap.Int("size", s.opts.ChannelLoadPoolSize))
	// }
	// err := s.channelLoadPool.Submit(func() {
	// 	channelId, channelType := wkutil.ChannelFromlKey(m.HandlerKey)
	// 	if channelId == "" {
	// 		s.Panic("channelId is empty", zap.String("handlerKey", m.HandlerKey))
	// 	}
	// 	_, err := s.loadOrCreateChannel(s.cancelCtx, channelId, channelType)
	// 	if err != nil {
	// 		s.Error("loadOrCreateChannel failed", zap.Error(err), zap.String("handlerKey", m.HandlerKey), zap.Uint64("from", m.From), zap.String("msgType", m.MsgType.String()))
	// 	}
	// 	s.channelLoadMapLock.Lock()
	// 	delete(s.channelLoadMap, m.HandlerKey)
	// 	s.channelLoadMapLock.Unlock()

	// 	s.Debug("active channel", zap.String("handlerKey", m.HandlerKey), zap.Uint64("from", m.From), zap.String("msgType", m.MsgType.String()))
	// })
	// if err != nil {
	// 	s.Error("channelLoadPool.Submit failed", zap.Error(err))
	// }
}

func (s *Server) newNodeByNodeInfo(nodeID uint64, addr string) *node {
	n := newNode(nodeID, s.serverUid(s.opts.NodeId), addr, s.opts)
	n.start()
	return n
}

func (s *Server) newSlot(st *pb.Slot) *slot {

	slot := newSlot(st, s)
	return slot

}

func (s *Server) addSlot(slot *pb.Slot) {
	st := s.newSlot(slot)
	s.slotManager.add(st)
	st.switchConfig(slot)
}

func (s *Server) addOrUpdateSlot(st *pb.Slot) {
	slot := s.slotManager.get(st.Id)
	if slot == nil {
		s.addSlot(st)
		return
	}
	slot.switchConfig(st)

}

func (s *Server) serverUid(id uint64) string {
	return fmt.Sprintf("%d", id)
}

func (s *Server) uidToServerId(uid string) uint64 {
	id, _ := strconv.ParseUint(uid, 10, 64)
	return id
}

func (s *Server) send(shardType ShardType, m reactor.Message) {
	if s.stopped.Load() {
		return
	}
	// 输出消息统计
	if shardType == ShardTypeSlot {
		traceOutgoingMessage(trace.ClusterKindSlot, m.MsgType, int64(m.Size()))
	} else if shardType == ShardTypeChannel {
		traceOutgoingMessage(trace.ClusterKindChannel, m.MsgType, int64(m.Size()))
	}

	node := s.nodeManager.node(m.To)
	if node == nil {
		s.Warn("send failed, node not exist", zap.Uint64("to", m.To), zap.String("msgType", m.MsgType.String()))
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
	msg := &proto.Message{
		MsgType: msgType,
		Content: data,
	}

	switch msg.MsgType {
	case MsgTypeChannel:
		trace.GlobalTrace.Metrics.Cluster().MessageOutgoingBytesAdd(trace.ClusterKindChannel, int64(msg.Size()))
		trace.GlobalTrace.Metrics.Cluster().MessageOutgoingCountAdd(trace.ClusterKindChannel, 1)
	case MsgTypeSlot:
		trace.GlobalTrace.Metrics.Cluster().MessageOutgoingBytesAdd(trace.ClusterKindSlot, int64(msg.Size()))
		trace.GlobalTrace.Metrics.Cluster().MessageOutgoingCountAdd(trace.ClusterKindSlot, 1)
	case MsgTypeConfig:
		trace.GlobalTrace.Metrics.Cluster().MessageOutgoingBytesAdd(trace.ClusterKindConfig, int64(msg.Size()))
		trace.GlobalTrace.Metrics.Cluster().MessageOutgoingCountAdd(trace.ClusterKindConfig, 1)
	}

	err = node.send(msg)
	if err != nil {
		s.Error("send failed", zap.Error(err))
		return
	}
}

func (s *Server) onSend(m reactor.Message) {

	s.opts.Send(ShardTypeConfig, m)
}

func (s *Server) onMessage(c gnet.Conn, m *proto.Message) {
	if s.stopped.Load() {
		return
	}

	start := time.Now()

	defer func() {
		cost := time.Since(start)
		if cost > time.Millisecond*50 {
			s.Warn("handle cluster message too cost...", zap.Duration("cost", cost), zap.Uint32("msgType", m.MsgType))
		}
	}()

	msgSize := int64(m.Size())

	trace.GlobalTrace.Metrics.System().IntranetIncomingAdd(msgSize) // 内网流量统计
	switch m.MsgType {
	case MsgTypeConfig:
		msg, err := reactor.UnmarshalMessage(m.Content)
		if err != nil {
			s.Error("UnmarshalMessage failed", zap.Error(err))
			return
		}
		trace.GlobalTrace.Metrics.Cluster().MessageIncomingCountAdd(trace.ClusterKindConfig, 1)
		trace.GlobalTrace.Metrics.Cluster().MessageIncomingBytesAdd(trace.ClusterKindConfig, msgSize)
		s.AddConfigMessage(msg)
	case MsgTypeSlot:

		msg, err := reactor.UnmarshalMessage(m.Content)
		if err != nil {
			s.Error("UnmarshalMessage failed", zap.Error(err))
			return
		}

		trace.GlobalTrace.Metrics.Cluster().MessageIncomingCountAdd(trace.ClusterKindSlot, 1)
		trace.GlobalTrace.Metrics.Cluster().MessageIncomingBytesAdd(trace.ClusterKindSlot, msgSize)
		s.AddSlotMessage(msg)
	case MsgTypeChannel:
		msg, err := reactor.UnmarshalMessage(m.Content)
		if err != nil {
			s.Error("UnmarshalMessage failed", zap.Error(err))
			return
		}
		trace.GlobalTrace.Metrics.Cluster().MessageIncomingCountAdd(trace.ClusterKindChannel, 1)
		trace.GlobalTrace.Metrics.Cluster().MessageIncomingBytesAdd(trace.ClusterKindChannel, msgSize)
		s.AddChannelMessage(msg)

	case MsgTypeChannelClusterConfigUpdate: // 频道配置更新
		go s.handleChannelClusterConfigUpdate(m)

	case MsgTypePing:
		ping, err := unmarshalPing(m.Content)
		if err != nil {
			s.Error("unmarshalPing failed", zap.Error(err))
			return
		}
		s.ping(ping)
	case MsgTypePong:
		pong, err := unmarshalPong(m.Content)
		if err != nil {
			s.Error("unmarshalPong failed", zap.Error(err))
			return
		}
		s.pong(pong)

	default:
		fromNodeId := s.uidToServerId(wkserver.GetUidFromContext(c))

		trace.GlobalTrace.Metrics.Cluster().MessageIncomingCountAdd(trace.ClusterKindUnknown, 1)
		trace.GlobalTrace.Metrics.Cluster().MessageIncomingBytesAdd(trace.ClusterKindUnknown, msgSize)
		if s.onMessageFnc != nil {
			s.onMessageFnc(fromNodeId, m) // 这里要注意，消息处理的时候不能阻塞，要不然整个分布式将变慢或卡住
		}
	}
}

func (s *Server) ping(p *ping) {

	pong := &pong{
		no:        p.no,
		from:      s.opts.NodeId,
		startMill: time.Now().UnixMilli(),
	}

	data := pong.marshal()

	msg := &proto.Message{
		MsgType: MsgTypePong,
		Content: data,
	}
	err := s.Send(p.from, msg)
	if err != nil {
		s.Error("send pong failed", zap.Error(err))
	}
}

func (s *Server) pong(p *pong) {
	s.testPingMapLock.Lock()
	ping, ok := s.testPings[p.no]
	if ok {
		ping.waitC <- p
	}
	s.testPingMapLock.Unlock()
}

// 获取频道所在的slotId
func (s *Server) getSlotId(v string) uint32 {
	var slotCount uint32 = s.clusterEventServer.SlotCount()
	if slotCount == 0 {
		slotCount = s.opts.SlotCount
	}
	return wkutil.GetSlotNum(int(slotCount), v)
}

// 是否需要加入集群
func (s *Server) needJoin() bool {
	if strings.TrimSpace(s.opts.Seed) == "" {
		return false
	}
	seedNodeId, _, _ := seedNode(s.opts.Seed) // New里已经验证过seed了  所以这里不必再处理error了
	seedNode := s.clusterEventServer.Node(seedNodeId)
	return seedNode == nil
}

func (s *Server) joinLoop() {
	seedNodeId, _, _ := seedNode(s.opts.Seed)
	req := &ClusterJoinReq{
		NodeId:     s.opts.NodeId,
		ServerAddr: s.opts.ServerAddr,
		Role:       s.opts.Role,
	}
	for {
		select {
		case <-time.After(time.Second * 2):
			resp, err := s.nodeManager.requestClusterJoin(seedNodeId, req)
			if err != nil {
				s.Error("requestClusterJoin failed", zap.Error(err), zap.Uint64("seedNodeId", seedNodeId))
				continue
			}
			if len(resp.Nodes) > 0 {
				for _, n := range resp.Nodes {
					s.addOrUpdateNode(n.NodeId, n.ServerAddr)
				}
			}
			return
		case <-s.stopper.ShouldStop():
			return
		}
	}
}

func seedNode(seed string) (uint64, string, error) {
	seedArray := strings.Split(seed, "@")
	if len(seedArray) < 2 {
		return 0, "", errors.New("seed format error")
	}
	seedNodeIDStr := seedArray[0]
	seedAddr := seedArray[1]
	seedNodeID, err := strconv.ParseUint(seedNodeIDStr, 10, 64)
	if err != nil {
		return 0, "", err
	}
	return seedNodeID, seedAddr, nil
}

func (s *Server) SendChannelClusterConfigUpdate(channelId string, channelType uint8, toNodeId uint64) error {

	cfg, err := s.loadOnlyChannelClusterConfig(channelId, channelType)
	if err != nil {
		s.Error("handleChannelConfigReq: Get failed", zap.Error(err), zap.String("channelId", channelId), zap.Uint8("channelType", channelType))
		return err
	}
	cfgData, err := cfg.Marshal()
	if err != nil {
		s.Error("handleChannelConfigReq: Marshal failed", zap.Error(err))
		return err
	}

	msg := &proto.Message{
		MsgType: MsgTypeChannelClusterConfigUpdate,
		Content: cfgData,
	}

	node := s.nodeManager.node(toNodeId)
	if node == nil {
		return fmt.Errorf("node not exist")
	}

	return node.send(msg)
}

func (s *Server) handleChannelClusterConfigUpdate(m *proto.Message) {

	cfg := wkdb.ChannelClusterConfig{}
	err := cfg.Unmarshal(m.Content)
	if err != nil {
		s.Error("handleChannelConfigUpdate: Unmarshal failed", zap.Error(err))
		return
	}

	s.UpdateChannelClusterConfig(cfg)

}

func (s *Server) UpdateChannelClusterConfig(cfg wkdb.ChannelClusterConfig) {
	channel, err := s.loadOrCreateChannel(s.cancelCtx, cfg.ChannelId, cfg.ChannelType)
	if err != nil {
		s.Error("handleChannelConfigUpdate: loadOrCreateChannel failed", zap.Error(err))
		return

	}
	err = channel.switchConfig(cfg) // 切换配置
	if err != nil {
		s.Error("handleChannelConfigUpdate: switchConfig failed", zap.Error(err))
		return
	}
	// err = s.opts.ChannelClusterStorage.Save(cfg)
	// if err != nil {
	// 	s.Error("handleChannelConfigUpdate: Save failed", zap.Error(err))
	// 	return
	// }
}

func (s *Server) addOrUpdateNode(id uint64, addr string) {
	if !s.nodeManager.exist(id) && strings.TrimSpace(addr) != "" {
		s.nodeManager.addNode(s.newNodeByNodeInfo(id, addr))
		return
	}
	n := s.nodeManager.node(id)
	if n != nil && n.addr != addr {
		s.nodeManager.removeNode(n.id)
		n.stop()
		s.nodeManager.addNode(s.newNodeByNodeInfo(id, addr))
	}
}
