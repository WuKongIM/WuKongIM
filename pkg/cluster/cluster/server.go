package cluster

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/cluster/clusterconfig"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/lni/goutils/syncutil"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

type Server struct {
	channelGroupManager  *channelGroupManager  // 频道管理，负责管理频道的集群
	slotManager          *slotManager          // 槽管理 负责管理槽的集群
	nodeManager          *nodeManager          // 节点管理 负责管理节点通信管道
	clusterEventListener *clusterEventListener // 分布式事件监听 用于监听集群事件
	localStorage         *localStorage         // 本地存储 用于存储本地数据
	server               *wkserver.Server      // 分布式通讯服务
	stopper              *syncutil.Stopper
	stopped              atomic.Bool
	cancelFunc           context.CancelFunc
	cancelCtx            context.Context
	onMessageFnc         func(from uint64, msg *proto.Message) // 上层处理消息的函数
	slotMigrateManager   *slotMigrateManager
	uptime               time.Time // 启动时间
	apiPrefix            string    // api前缀
	defaultMonitor       *DefaultMonitor
	// 其他
	opts *Options
	wklog.Log
}

func New(nodeId uint64, opts *Options) *Server {

	opts.NodeID = nodeId
	s := &Server{
		slotManager:          newSlotManager(opts),
		nodeManager:          newNodeManager(opts),
		clusterEventListener: newClusterEventListener(opts),
		stopper:              syncutil.NewStopper(),
		opts:                 opts,
		Log:                  wklog.NewWKLog(fmt.Sprintf("clusterServer[%d]", opts.NodeID)),
	}
	s.slotMigrateManager = newSlotMigrateManager(s)
	opts.nodeOnlineFnc = s.nodeCliOnline
	opts.existSlotMigrateFnc = s.slotMigrateManager.exist
	opts.removeSlotMigrateFnc = s.slotMigrateManager.remove
	opts.requestSlotLogInfo = s.requestSlotLogInfo
	s.localStorage = newLocalStorage(s.opts)

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

	s.defaultMonitor = NewDefaultMonitor(s)

	return s
}

func (s *Server) nodeCliOnline(nodeID uint64) (bool, error) {
	if s.opts.NodeID == nodeID {
		return true, nil
	}
	node := s.nodeManager.node(nodeID)
	if node == nil {
		s.Error("node not found", zap.Uint64("nodeID", nodeID))
		return false, fmt.Errorf("node[%d] not found", nodeID)
	}
	return node.online(), nil
}

func (s *Server) requestSlotLogInfo(ctx context.Context, nodeId uint64, req *SlotLogInfoReq) (*SlotLogInfoResp, error) {
	node := s.nodeManager.node(nodeId)
	if node == nil {
		return nil, ErrNodeNotFound
	}
	return node.requestSlotLogInfo(ctx, req)
}

func (s *Server) Start() error {
	s.uptime = time.Now()

	err := s.opts.ShardLogStorage.Open()
	if err != nil {
		return err
	}

	// 监听消息
	s.server.OnMessage(func(conn wknet.Conn, msg *proto.Message) {
		from := s.nodeIdByServerUid(conn.UID())
		s.handleMessage(from, msg)
	})

	// 请求路由设置
	s.setRoutes()

	// 服务开始
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

	slots := s.clusterEventListener.localAllSlot()
	if len(slots) > 0 {
		err = s.addSlots(slots) // 添加槽
		if err != nil {
			return err
		}
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

	// 根据需要是否加入集群
	s.stopper.RunWorker(s.joinClusterIfNeed)

	s.slotMigrateManager.start()
	return nil
}

func (s *Server) Stop() {
	s.stopped.Store(true)
	s.cancelFunc()
	s.slotMigrateManager.stop()
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

func (s *Server) joinClusterIfNeed() {
	if strings.TrimSpace(s.opts.Seed) == "" || s.joined() {
		return
	}
	// 添加种子节点
	seedNodeId, seedAddr, err := SeedNode(s.opts.Seed)
	if err != nil {
		s.Panic("SeedNode error", zap.Error(err))
	}
	s.addNode(seedNodeId, seedAddr)

	tryMaxCount := 10
	for {
		resp, err := s.requestJoinCluster(seedNodeId)
		if err != nil {
			s.Error("requestJoinCluster error", zap.Error(err))
		} else {
			if len(resp.Nodes) > 0 {
				for _, n := range resp.Nodes {
					s.addNode(n.NodeId, n.ServerAddr)
				}
			}
			s.Info("requestJoinCluster success", zap.Uint64("seedNodeId", seedNodeId))
			return
		}
		time.Sleep(time.Second * 2)
		tryMaxCount--
		if tryMaxCount <= 0 {
			s.Panic("requestJoinCluster failed", zap.Error(err))
		}
	}

}

func SeedNode(seed string) (uint64, string, error) {
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

func (s *Server) requestJoinCluster(seedNodeId uint64) (*ClusterJoinResp, error) {
	return s.nodeManager.requestClusterJoin(seedNodeId, &ClusterJoinReq{
		NodeId:     s.opts.NodeID,
		ServerAddr: s.opts.ServerAddr,
		Role:       s.opts.Role,
	})
}

// 是否已加入集群
func (s *Server) joined() bool {
	cfg := s.getClusterConfig()
	for _, n := range cfg.Nodes {
		if n.Id == s.opts.NodeID {
			return true
		}
	}
	return false
}

// 监听分布式事件
func (s *Server) listenClusterEvent() {
	for !s.stopped.Load() {
		event := s.clusterEventListener.wait()
		if !IsEmptyClusterEvent(event) {
			s.handleClusterEvent(event)
		}
	}
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
	default:

		if s.onMessageFnc != nil {
			s.onMessageFnc(from, m)
		}
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

func (s *Server) MustWaitAllSlotLeaderReady(timeout time.Duration) {
	err := s.WaitAllSlotLeaderReady(timeout)
	if err != nil {
		s.Panic("WaitAllSlotLeaderReady failed!", zap.Error(err))
	}
}

func (s *Server) WaitAllSlotLeaderReady(timeout time.Duration) error {
	timeoutCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	tick := time.NewTicker(time.Millisecond * 20)
	for {
		select {
		case <-timeoutCtx.Done():
			return timeoutCtx.Err()
		case <-tick.C:
			if len(s.clusterEventListener.localConfg.Slots) == 0 {
				continue
			}
			all := true
			for _, slot := range s.clusterEventListener.localConfg.Slots {
				if slot.Leader == 0 {
					all = false
					break
				}
			}
			if all {
				return nil
			}
		case <-s.stopper.ShouldStop():
			return errors.New("server stopped")
		}
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

func (s *Server) handleSlotMsg(_ uint64, m *proto.Message) {

	msg, err := UnmarshalMessage(m.Content)
	if err != nil {
		s.Error("unmarshal slot message error", zap.Error(err))
		return
	}
	slotId := GetSlotId(msg.ShardNo, s.Log)
	s.slotManager.handleMessage(slotId, msg.Message)
}

func (s *Server) handleChannelMsg(_ uint64, m *proto.Message) {
	msg, err := UnmarshalMessage(m.Content)
	if err != nil {
		s.Error("unmarshal slot message error", zap.Error(err))
		return
	}

	// s.Info("收到频道消息", zap.String("msgType", msg.MsgType.String()), zap.Uint64("from", msg.From), zap.Uint32("term", msg.Term), zap.Uint64("to", msg.To), zap.String("shardNo", msg.ShardNo), zap.Uint64("index", msg.Index), zap.Uint64("committed", msg.CommittedIndex))

	channelID, channelType := ChannelFromChannelKey(msg.ShardNo)
	err = s.channelGroupManager.handleMessage(channelID, channelType, msg.Message)
	if err != nil {
		s.Error("handle channel message error", zap.Error(err), zap.String("shardNo", msg.ShardNo))
		return
	}
}
