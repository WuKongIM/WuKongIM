package server

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/api"
	channelevent "github.com/WuKongIM/WuKongIM/internal/channel/event"
	channelhandler "github.com/WuKongIM/WuKongIM/internal/channel/handler"
	"github.com/WuKongIM/WuKongIM/internal/common"
	"github.com/WuKongIM/WuKongIM/internal/eventbus"
	"github.com/WuKongIM/WuKongIM/internal/ingress"
	"github.com/WuKongIM/WuKongIM/internal/manager"
	"github.com/WuKongIM/WuKongIM/internal/options"
	pusherevent "github.com/WuKongIM/WuKongIM/internal/pusher/event"
	pusherhandler "github.com/WuKongIM/WuKongIM/internal/pusher/handler"
	"github.com/WuKongIM/WuKongIM/internal/service"
	userevent "github.com/WuKongIM/WuKongIM/internal/user/event"
	userhandler "github.com/WuKongIM/WuKongIM/internal/user/handler"
	"github.com/WuKongIM/WuKongIM/internal/webhook"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/clusterconfig/pb"
	cluster "github.com/WuKongIM/WuKongIM/pkg/cluster/clusterserver"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/clusterstore"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/trace"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/WuKongIM/WuKongIM/version"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/gin-gonic/gin"
	"github.com/judwhite/go-svc"
	"go.etcd.io/etcd/pkg/v3/idutil"
	"go.uber.org/zap"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

type Server struct {
	opts          *options.Options  // 配置
	wklog.Log                       // 日志
	clusterServer *cluster.Server   // 分布式服务实现
	reqIDGen      *idutil.Generator // 请求ID生成器
	ctx           context.Context
	cancel        context.CancelFunc
	start         time.Time           // 服务开始时间
	store         *clusterstore.Store // 存储相关接口
	engine        *wknet.Engine       // 长连接引擎
	// userReactor    *userReactor    // 用户的reactor，用于处理用户的行为逻辑
	trace      *trace.Trace // 监控
	demoServer *DemoServer  // demo server
	datasource IDatasource  // 数据源
	apiServer  *api.Server  // api服务
	ingress    *ingress.Ingress

	commonService *common.Service // 通用服务
	// 管理者
	retryManager        *manager.RetryManager        // 消息重试管理
	conversationManager *manager.ConversationManager // 会话管理
	tagManager          *manager.TagManager          // tag管理
	webhook             *webhook.Webhook

	// 用户事件池
	userHandler   *userhandler.Handler
	userEventPool *userevent.EventPool

	// 频道事件池
	channelHandler   *channelhandler.Handler
	channelEventPool *channelevent.EventPool

	// push事件池
	pushHandler   *pusherhandler.Handler
	pushEventPool *pusherevent.EventPool
}

func New(opts *options.Options) *Server {
	now := time.Now().UTC()

	options.G = opts

	s := &Server{
		opts:     opts,
		Log:      wklog.NewWKLog("Server"),
		reqIDGen: idutil.NewGenerator(uint16(opts.Cluster.NodeId), time.Now()),
		start:    now,
	}
	// 配置检查
	err := opts.Check()
	if err != nil {
		s.Panic("config check error", zap.Error(err))
	}

	s.ingress = ingress.New()

	// user event pool
	s.userHandler = userhandler.NewHandler()
	s.userEventPool = userevent.NewEventPool(s.userHandler)
	eventbus.RegisterUser(s.userEventPool)

	// channel event pool
	s.channelHandler = channelhandler.NewHandler()
	s.channelEventPool = channelevent.NewEventPool(s.channelHandler)
	eventbus.RegisterChannel(s.channelEventPool)

	// push event pool
	s.pushHandler = pusherhandler.NewHandler()
	s.pushEventPool = pusherevent.NewEventPool(s.pushHandler)
	eventbus.RegisterPusher(s.pushEventPool)

	s.ctx, s.cancel = context.WithCancel(context.Background())

	s.trace = trace.New(
		s.ctx,
		trace.NewOptions(
			trace.WithServiceName(s.opts.Trace.ServiceName),
			trace.WithServiceHostName(s.opts.Trace.ServiceHostName),
			trace.WithPrometheusApiUrl(s.opts.Trace.PrometheusApiUrl),
		))
	trace.SetGlobalTrace(s.trace)

	gin.SetMode(opts.GinMode)

	// 初始化存储
	storeOpts := clusterstore.NewOptions(s.opts.Cluster.NodeId)
	storeOpts.DataDir = path.Join(s.opts.DataDir, "db")
	storeOpts.SlotCount = uint32(s.opts.Cluster.SlotCount)
	storeOpts.GetSlotId = s.getSlotId
	storeOpts.IsCmdChannel = opts.IsCmdChannel
	storeOpts.Db.ShardNum = s.opts.Db.ShardNum
	storeOpts.Db.MemTableSize = s.opts.Db.MemTableSize
	s.store = clusterstore.NewStore(storeOpts)

	service.Store = s.store

	// 数据源
	s.datasource = NewDatasource(s)

	// 初始化长连接引擎
	s.engine = wknet.NewEngine(
		wknet.WithAddr(s.opts.Addr),
		wknet.WithWSAddr(s.opts.WSAddr),
		wknet.WithWSSAddr(s.opts.WSSAddr),
		wknet.WithWSTLSConfig(s.opts.WSTLSConfig),
		wknet.WithOnReadBytes(func(n int) {
			trace.GlobalTrace.Metrics.System().ExtranetIncomingAdd(int64(n))
		}),
		wknet.WithOnWirteBytes(func(n int) {
			trace.GlobalTrace.Metrics.System().ExtranetOutgoingAdd(int64(n))
		}),
	)

	s.demoServer = NewDemoServer(s) // demo server

	service.Webhook = webhook.New()

	s.webhook = webhook.New()
	service.Webhook = s.webhook
	// manager
	s.retryManager = manager.NewRetryManager()               // 消息重试管理
	s.conversationManager = manager.NewConversationManager() // 会话管理
	s.tagManager = manager.NewTagManager(16, func() uint64 {
		return service.Cluster.NodeVersion()
	})
	// register service
	service.ConnManager = manager.NewConnManager(18) // 连接管理
	service.ConversationManager = s.conversationManager
	service.RetryManager = s.retryManager
	service.TagManager = s.tagManager
	service.SystemAccountManager = manager.NewSystemAccountManager() // 系统账号管理

	s.commonService = common.NewService()
	service.CommonService = s.commonService

	// 初始化分布式服务
	initNodes := make(map[uint64]string)
	if len(s.opts.Cluster.InitNodes) > 0 {
		for _, node := range s.opts.Cluster.InitNodes {
			serverAddr := strings.ReplaceAll(node.ServerAddr, "tcp://", "")
			initNodes[node.Id] = serverAddr
		}
	}
	role := pb.NodeRole_NodeRoleReplica
	if s.opts.Cluster.Role == options.RoleProxy {
		role = pb.NodeRole_NodeRoleProxy
	}
	clusterServer := cluster.New(
		cluster.NewOptions(
			cluster.WithNodeId(s.opts.Cluster.NodeId),
			cluster.WithAddr(strings.ReplaceAll(s.opts.Cluster.Addr, "tcp://", "")),
			cluster.WithDataDir(path.Join(opts.DataDir, "cluster")),
			cluster.WithSlotCount(uint32(s.opts.Cluster.SlotCount)),
			cluster.WithInitNodes(initNodes),
			cluster.WithSeed(s.opts.Cluster.Seed),
			cluster.WithRole(role),
			cluster.WithServerAddr(s.opts.Cluster.ServerAddr),
			cluster.WithMessageLogStorage(s.store.GetMessageShardLogStorage()),
			cluster.WithApiServerAddr(s.opts.Cluster.APIUrl),
			cluster.WithChannelMaxReplicaCount(s.opts.Cluster.ChannelReplicaCount),
			cluster.WithSlotMaxReplicaCount(uint32(s.opts.Cluster.SlotReplicaCount)),
			cluster.WithLogLevel(s.opts.Logger.Level),
			cluster.WithAppVersion(version.Version),
			cluster.WithDB(s.store.DB()),
			cluster.WithSlotDbShardNum(s.opts.Db.SlotShardNum),
			cluster.WithOnSlotApply(func(slotId uint32, logs []replica.Log) error {

				return s.store.OnMetaApply(slotId, logs)
			}),
			cluster.WithChannelClusterStorage(clusterstore.NewChannelClusterConfigStore(s.store)),
			cluster.WithElectionIntervalTick(s.opts.Cluster.ElectionIntervalTick),
			cluster.WithHeartbeatIntervalTick(s.opts.Cluster.HeartbeatIntervalTick),
			cluster.WithTickInterval(s.opts.Cluster.TickInterval),
			cluster.WithChannelReactorSubCount(s.opts.Cluster.ChannelReactorSubCount),
			cluster.WithSlotReactorSubCount(s.opts.Cluster.SlotReactorSubCount),
			cluster.WithPongMaxTick(s.opts.Cluster.PongMaxTick),
			cluster.WithAuth(s.opts.Auth),
			cluster.WithServiceName(s.opts.Trace.ServiceName),
			cluster.WithLokiUrl(s.opts.Logger.Loki.Url),
			cluster.WithLokiJob(s.opts.Logger.Loki.Job),
		),

		// cluster.WithOnChannelMetaApply(func(channelID string, channelType uint8, logs []replica.Log) error {
		// 	return s.store.OnMetaApply(channelID, channelType, logs)
		// }),
	)
	service.Cluster = clusterServer
	s.clusterServer = clusterServer
	storeOpts.Cluster = clusterServer

	clusterServer.OnMessage(func(fromNodeId uint64, msg *proto.Message) {
		s.handleClusterMessage(fromNodeId, msg)
	})

	s.apiServer = api.New()
	return s
}

func (s *Server) Init(env svc.Environment) error {
	if env.IsWindowsService() {
		dir := filepath.Dir(os.Args[0])
		return os.Chdir(dir)
	}
	return nil
}

func (s *Server) Start() error {

	fmt.Println(`
	
	__      __       ____  __.                    .___   _____   
	/  \    /  \__ __|    |/ _|____   ____    ____ |   | /     \  
	\   \/\/   /  |  \      < /  _ \ /    \  / ___\|   |/  \ /  \ 
	 \        /|  |  /    |  (  <_> )   |  \/ /_/  >   /    Y    \
	  \__/\  / |____/|____|__ \____/|___|  /\___  /|___\____|__  /
		   \/                \/          \//_____/             \/ 						  
							  
	`)
	s.Info("WuKongIM is Starting...")
	s.Info(fmt.Sprintf("  Using config file:  %s", s.opts.ConfigFileUsed()))
	s.Info(fmt.Sprintf("  Mode:  %s", s.opts.Mode))
	s.Info(fmt.Sprintf("  Version:  %s", version.Version))
	s.Info(fmt.Sprintf("  Git:  %s", fmt.Sprintf("%s-%s", version.CommitDate, version.Commit)))
	s.Info(fmt.Sprintf("  Go build:  %s", runtime.Version()))
	s.Info(fmt.Sprintf("  DataDir:  %s", s.opts.DataDir))

	s.Info(fmt.Sprintf("Listening  for TCP client on %s", s.opts.Addr))
	s.Info(fmt.Sprintf("Listening  for WS client on %s", s.opts.WSAddr))
	if s.opts.WSSAddr != "" {
		s.Info(fmt.Sprintf("Listening  for WSS client on %s", s.opts.WSSAddr))
	}
	s.Info(fmt.Sprintf("Listening  for Manager http api on %s", fmt.Sprintf("http://%s", s.opts.HTTPAddr)))

	if s.opts.Manager.On {
		s.Info(fmt.Sprintf("Listening  for Manager on %s", s.opts.Manager.Addr))
	}

	defer s.Info("Server is ready")

	var err error

	err = s.commonService.Start()
	if err != nil {
		return err
	}

	s.ingress.SetRoutes()

	// 重试管理
	if err = s.retryManager.Start(); err != nil {
		return err
	}

	// tag管理
	if err = s.tagManager.Start(); err != nil {
		return err
	}

	err = s.trace.Start()
	if err != nil {
		return err

	}

	err = s.store.Open()
	if err != nil {
		return err
	}

	err = service.Cluster.Start()
	if err != nil {
		return err
	}

	s.engine.OnConnect(s.onConnect)
	s.engine.OnData(s.onData)
	s.engine.OnClose(s.onClose)

	err = s.engine.Start()
	if err != nil {
		return err
	}

	if s.opts.Demo.On {
		s.demoServer.Start()
	}

	if s.opts.Conversation.On {
		err = s.conversationManager.Start()
		if err != nil {
			return err
		}
	}

	err = s.webhook.Start()
	if err != nil {
		s.Panic("webhook start error", zap.Error(err))
	}

	if err = s.apiServer.Start(); err != nil {
		return err
	}

	err = s.userEventPool.Start()
	if err != nil {
		return err
	}

	err = s.channelEventPool.Start()
	if err != nil {
		return err
	}

	err = s.pushEventPool.Start()
	if err != nil {
		return err
	}

	return nil
}

func (s *Server) StopNoErr() {
	err := s.Stop()
	if err != nil {
		s.Error("Server stop error", zap.Error(err))
	}
}

func (s *Server) Stop() error {

	s.cancel()

	s.userEventPool.Stop()

	s.channelEventPool.Stop()

	s.pushEventPool.Stop()

	s.apiServer.Stop()

	s.retryManager.Stop()

	s.commonService.Stop()

	if s.opts.Conversation.On {
		s.conversationManager.Stop()
	}

	service.Cluster.Stop()

	if s.opts.Demo.On {
		s.demoServer.Stop()
	}

	err := s.engine.Stop()
	if err != nil {
		s.Error("engine stop error", zap.Error(err))
	}
	s.trace.Stop()

	s.store.Close()

	s.tagManager.Stop()

	s.webhook.Stop()

	s.Info("Server is stopped")

	return nil
}

// 等待分布式就绪
func (s *Server) MustWaitClusterReady(timeout time.Duration) {
	service.Cluster.MustWaitClusterReady(timeout)
}

func (s *Server) MustWaitAllSlotsReady(timeout time.Duration) {
	service.Cluster.MustWaitAllSlotsReady(timeout)
}

// 获取分布式配置
func (s *Server) GetClusterConfig() *pb.Config {
	return s.clusterServer.GetConfig()
}

// 迁移槽
func (s *Server) MigrateSlot(slotId uint32, fromNodeId, toNodeId uint64) error {

	return s.clusterServer.MigrateSlot(slotId, fromNodeId, toNodeId)
}

func (s *Server) getSlotId(v string) uint32 {
	return service.Cluster.GetSlotId(v)
}

func (s *Server) onConnect(conn wknet.Conn) error {
	conn.SetMaxIdle(time.Second * 2) // 在认证之前，连接最多空闲2秒
	s.trace.Metrics.App().ConnCountAdd(1)

	service.ConnManager.AddConn(conn)

	return nil
}

// 解析代理协议，获取真实IP
// func (s *Server) handleProxyProto(buff []byte) error {
// 	remoteAddr, size, err := parseProxyProto(buff)
// 	if err != nil && err != ErrNoProxyProtocol {
// 		s.Warn("Failed to parse proxy proto", zap.Error(err))
// 	}
// 	if remoteAddr != nil {
// 		conn.SetRemoteAddr(remoteAddr)
// 		s.Debug("parse proxy proto success", zap.String("remoteAddr", remoteAddr.String()))
// 	}
// 	return nil
// }

func (s *Server) onClose(conn wknet.Conn) {

	s.trace.Metrics.App().ConnCountAdd(-1)
	connCtxObj := conn.Context()
	if connCtxObj != nil {
		connCtx := connCtxObj.(*eventbus.Conn)
		eventbus.User.RemoveConn(connCtx)
		if connCtx.Auth {
			deviceOnlineCount := eventbus.User.ConnCountByDeviceFlag(connCtx.Uid, connCtx.DeviceFlag)
			totalOnlineCount := eventbus.User.ConnCountByUid(connCtx.Uid)
			service.Webhook.Offline(connCtx.Uid, wkproto.DeviceFlag(connCtx.DeviceFlag), connCtx.ConnId, deviceOnlineCount, totalOnlineCount) // 触发离线webhook
		}
	}
	service.ConnManager.RemoveConn(conn)
}

func (s *Server) WithRequestTimeout() (context.Context, context.CancelFunc) {
	return context.WithTimeout(s.ctx, s.opts.Cluster.ReqTimeout)
}
