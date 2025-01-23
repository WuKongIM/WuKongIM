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

	"github.com/WuKongIM/WuKongIM/pkg/cluster/channel"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/node/clusterconfig"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/node/event"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/node/types"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/slot"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/store"
	"github.com/WuKongIM/WuKongIM/pkg/keylock"
	rafttype "github.com/WuKongIM/WuKongIM/pkg/raft/types"
	"github.com/WuKongIM/WuKongIM/pkg/trace"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"github.com/lni/goutils/syncutil"
	"github.com/panjf2000/ants/v2"
	"github.com/panjf2000/gnet/v2"
	"go.uber.org/zap"
)

type Server struct {
	// 分布式配置服务
	cfgServer *clusterconfig.Server
	// 分布式事件
	eventServer *event.Server
	// 槽分布式服务
	slotServer *slot.Server
	// channel分布式服务
	channelServer *channel.Server
	// 分布式存储
	store *store.Store
	// 节点管理
	nodeManager *nodeManager
	//  节点之间通讯的网络服务
	netServer *wkserver.Server
	// rpc服务
	rpcServer *rpcServer
	rpcClient *rpcClient
	// 数据库
	db wkdb.DB
	// 配置
	opts      *Options
	apiPrefix string // api前缀
	wklog.Log
	cancelCtx     context.Context
	cancelFnc     context.CancelFunc
	onMessageFnc  func(fromNodeId uint64, msg *proto.Message)
	onMessagePool *ants.Pool
	uptime        time.Time // 服务启动时间
	stopper       *syncutil.Stopper

	channelKeyLock *keylock.KeyLock // 频道锁

	sync.Mutex
}

func New(opts *Options) *Server {
	s := &Server{
		opts:           opts,
		nodeManager:    newNodeManager(opts),
		Log:            wklog.NewWKLog("cluster"),
		channelKeyLock: keylock.NewKeyLock(),
		uptime:         time.Now(),
		stopper:        syncutil.NewStopper(),
	}
	s.cancelCtx, s.cancelFnc = context.WithCancel(context.Background())
	// 初始化传输层
	if opts.SlotTransport == nil {
		opts.SlotTransport = newSlotTransport(s)
	}
	if opts.ChannelTransport == nil {
		opts.ChannelTransport = newChannelTransport(s)
	}
	if opts.ConfigOptions.Transport == nil {
		opts.ConfigOptions.Transport = newNodeTransport(s)
	}

	s.onMessagePool, _ = ants.NewPool(10000, ants.WithNonblocking(true), ants.WithPanicHandler(func(err interface{}) {
		s.Foucs("message pool panic", zap.Stack("stack"), zap.Any("err", err))
	}))

	s.rpcServer = newRpcServer(s)
	s.rpcClient = newRpcClient(s)

	s.db = wkdb.NewWukongDB(
		wkdb.NewOptions(
			wkdb.WithShardNum(opts.DB.WKDbShardNum),
			wkdb.WithDir(path.Join(opts.DataDir, "db")),
			wkdb.WithNodeId(opts.ConfigOptions.NodeId),
			wkdb.WithMemTableSize(opts.DB.WKDbMemTableSize),
			wkdb.WithSlotCount(int(opts.ConfigOptions.SlotCount)),
		),
	)

	// 节点之间通讯的网络服务
	s.netServer = wkserver.New(
		opts.Addr,
		wkserver.WithMessagePoolOn(false),
		wkserver.WithOnRequest(func(conn gnet.Conn, req *proto.Request) {
			trace.GlobalTrace.Metrics.System().IntranetIncomingAdd(int64(len(req.Body)))
		}),
		wkserver.WithOnResponse(func(conn gnet.Conn, resp *proto.Response) {
			trace.GlobalTrace.Metrics.System().IntranetOutgoingAdd(int64(len(resp.Body)))
		}),
	)
	s.netServer.OnMessage(s.onMessage)

	// 节点分布式配置服务
	s.cfgServer = clusterconfig.New(opts.ConfigOptions)

	//节点事件服务
	s.eventServer = event.NewServer(s, opts.ConfigOptions, s.cfgServer)

	// 槽分布式服务
	s.slotServer = slot.NewServer(slot.NewOptions(
		slot.WithNodeId(opts.ConfigOptions.NodeId),
		slot.WithDataDir(path.Join(opts.DataDir, "cluster")),
		slot.WithTransport(opts.SlotTransport),
		slot.WithNode(s.cfgServer),
		slot.WithOnApply(s.slotApplyLogs),
		slot.WithOnSaveConfig(s.onSaveSlotConfig),
	))

	// 频道分布式服务
	s.channelServer = channel.NewServer(channel.NewOptions(
		channel.WithNodeId(opts.ConfigOptions.NodeId),
		channel.WithTransport(opts.ChannelTransport),
		channel.WithSlot(s.slotServer),
		channel.WithNode(s.cfgServer),
		channel.WithCluster(s),
		channel.WithDB(s.db),
		channel.WithRPC(s.rpcClient),
		channel.WithOnSaveConfig(s.onSaveChannelConfig),
	))

	// 分布式存储
	s.store = store.New(store.NewOptions(
		store.WithNodeId(opts.ConfigOptions.NodeId),
		store.WithSlot(s.slotServer),
		store.WithChannel(s.channelServer),
		store.WithDB(s.db),
		store.WithIsCmdChannel(opts.IsCmdChannel),
	))

	// 添加事件监听
	s.cfgServer.AddEventListener(s.slotServer)
	s.cfgServer.AddEventListener(s)

	return s
}

func (s *Server) Start() error {

	s.channelKeyLock.StartCleanLoop()

	err := s.db.Open()
	if err != nil {
		return err
	}

	err = s.store.Start()
	if err != nil {
		return err
	}

	s.rpcServer.setRoutes()

	err = s.netServer.Start()
	if err != nil {
		return err
	}

	err = s.slotServer.Start()
	if err != nil {
		return err
	}

	err = s.eventServer.Start()
	if err != nil {
		return err
	}

	err = s.cfgServer.Start()
	if err != nil {
		return err
	}

	err = s.channelServer.Start()
	if err != nil {
		return err
	}

	// 添加或更新节点
	cfg := s.cfgServer.GetClusterConfig()
	if len(cfg.Nodes) == 0 {
		if len(s.opts.ConfigOptions.InitNodes) > 0 {
			s.addOrUpdateNodes(s.opts.ConfigOptions.InitNodes)
		} else if strings.TrimSpace(s.opts.Seed) != "" {
			nodeMap := make(map[uint64]string)
			seedNodeId, addr, err := seedNode(s.opts.Seed)
			if err != nil {
				return err
			}
			nodeMap[seedNodeId] = addr
			s.addOrUpdateNodes(nodeMap)
		}

	} else {
		nodeMap := make(map[uint64]string)
		for _, node := range cfg.Nodes {
			nodeMap[node.Id] = node.ClusterAddr
		}
		s.addOrUpdateNodes(nodeMap)
	}

	// 如果有新加入的节点 则执行加入逻辑
	join, err := s.needJoin()
	if err != nil {
		return err
	}
	if join { // 需要加入集群
		s.stopper.RunWorker(s.joinLoop)
	}

	return nil
}

func (s *Server) Stop() {
	s.eventServer.Stop()
	s.cfgServer.Stop()
	s.slotServer.Stop()
	s.channelServer.Stop()
	s.netServer.Stop()
	s.store.Stop()
	s.db.Close()
	s.channelKeyLock.StopCleanLoop()
}

func (s *Server) NodeStep(event rafttype.Event) {
	s.eventServer.Step(event)
}

func (s *Server) AddSlotEvent(shardNo string, event rafttype.Event) {
	s.slotServer.AddEvent(shardNo, event)
}

func (s *Server) GetConfigServer() *clusterconfig.Server {
	return s.cfgServer
}

func (s *Server) WaitAllSlotReady(ctx context.Context, slotCount int) error {

	return s.slotServer.WaitAllSlotReady(ctx, slotCount)
}

func (s *Server) ProposeToSlotUntilApplied(slotId uint32, data []byte) (*rafttype.ProposeResp, error) {

	return s.slotServer.ProposeUntilApplied(slotId, data)
}

func (s *Server) GetStore() *store.Store {
	return s.store
}

// 配置改变
func (s *Server) OnConfigChange(cfg *types.Config) {

	nodeMap := make(map[uint64]string)
	for _, node := range cfg.Nodes {
		nodeMap[node.Id] = node.ClusterAddr
	}
	s.addOrUpdateNodes(nodeMap)
}

func (s *Server) slotApplyLogs(slotId uint32, logs []rafttype.Log) error {
	err := s.store.ApplySlotLogs(slotId, logs)
	if err != nil {
		s.Panic("apply slot logs failed", zap.Uint32("slotId", slotId), zap.Error(err))
		return err
	}
	return nil
}

func (s *Server) addOrUpdateNodes(nodeMap map[uint64]string) {
	s.Lock()
	defer s.Unlock()

	if len(nodeMap) == 0 {
		return
	}
	for nodeId, addr := range nodeMap {

		if nodeId == s.opts.ConfigOptions.NodeId {
			continue
		}

		existNode := s.nodeManager.node(nodeId)

		if existNode != nil {
			if existNode.addr == addr {
				continue
			} else {
				existNode.stop()
				s.nodeManager.removeNode(existNode.id)
			}
		}

		n := newNode(nodeId, s.serverUid(s.opts.ConfigOptions.NodeId), addr, s.opts)
		n.start()
		s.nodeManager.addNode(n)
	}
}

func (s *Server) serverUid(id uint64) string {
	return fmt.Sprintf("%d", id)
}

// 获取频道所在的slotId
func (s *Server) getSlotId(v string) uint32 {
	var slotCount uint32 = s.cfgServer.SlotCount()
	if slotCount == 0 {
		slotCount = s.opts.ConfigOptions.SlotCount
	}
	return wkutil.GetSlotNum(int(slotCount), v)
}

func (s *Server) uidToServerId(uid string) uint64 {
	id, _ := strconv.ParseUint(uid, 10, 64)
	return id
}

// 保存槽分布式配置（事件）
func (s *Server) onSaveSlotConfig(slotId uint32, cfg rafttype.Config) error {

	slot := s.cfgServer.Slot(slotId)
	if slot == nil {
		s.Error("slot not found", zap.Uint32("slotId", slotId))
		return errors.New("slot not found")
	}
	cloneSlot := slot.Clone()
	// 更新slot的配置
	s.updateSlotByConfig(cloneSlot, cfg)

	// 提案槽更新（槽更新会触发分布式配置事件，事件会触发slot更新最新的配置，所以这里只需要提案即可，不需要再进行配置切换）
	err := s.cfgServer.ProposeSlots([]*types.Slot{cloneSlot})
	if err != nil {
		s.Error("onSaveSlotConfig: propose slot failed", zap.Uint32("slotId", slotId), zap.Error(err))
		return err
	}

	return nil
}

// 保存频道分布式配置（事件）
func (s *Server) onSaveChannelConfig(channelId string, channelType uint8, cfg rafttype.Config) error {

	channelCfg, err := s.GetOrCreateChannelClusterConfigFromSlotLeader(channelId, channelType)
	if err != nil {
		s.Error("onSaveChannelConfig: get channel cluster config failed", zap.String("channelId", channelId), zap.Uint8("channelType", channelType), zap.Error(err))
		return err
	}

	// 更新配置
	s.updateChannelCfgByConfig(&channelCfg, cfg)

	// 保存配置
	version, err := s.store.SaveChannelClusterConfig(channelCfg)
	if err != nil {
		s.Error("onSaveChannelConfig: save channel cluster config failed", zap.String("channelId", channelId), zap.Uint8("channelType", channelType), zap.Error(err))
		return err
	}
	channelCfg.ConfVersion = version

	// 切换配置
	if channelCfg.LeaderId == s.opts.ConfigOptions.NodeId {
		err = s.channelServer.SwitchConfig(channelCfg.ChannelId, channelCfg.ChannelType, channelCfg)
		if err != nil {
			s.Error("onSaveChannelConfig: switch config failed", zap.String("channelId", channelId), zap.Uint8("channelType", channelType), zap.Error(err))
			return err
		}
	} else {
		return s.rpcClient.RequestChannelSwitchConfig(channelCfg.LeaderId, channelCfg)
	}

	return nil
}

func (s *Server) updateSlotByConfig(st *types.Slot, cfg rafttype.Config) {
	st.Leader = cfg.Leader
	st.Term = cfg.Term
	st.Replicas = cfg.Replicas
	st.Learners = cfg.Learners
	st.MigrateFrom = cfg.MigrateFrom
	st.MigrateTo = cfg.MigrateTo
}

func (s *Server) updateChannelCfgByConfig(cfg *wkdb.ChannelClusterConfig, c rafttype.Config) {
	cfg.LeaderId = c.Leader
	cfg.Term = c.Term
	cfg.Replicas = c.Replicas
	cfg.Learners = c.Learners
	cfg.MigrateFrom = c.MigrateFrom
	cfg.MigrateTo = c.MigrateTo
}
