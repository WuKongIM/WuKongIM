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
	"github.com/WuKongIM/WuKongIM/internal/plugin"
	pusherevent "github.com/WuKongIM/WuKongIM/internal/pusher/event"
	pusherhandler "github.com/WuKongIM/WuKongIM/internal/pusher/handler"
	"github.com/WuKongIM/WuKongIM/internal/service"
	userevent "github.com/WuKongIM/WuKongIM/internal/user/event"
	userhandler "github.com/WuKongIM/WuKongIM/internal/user/handler"
	"github.com/WuKongIM/WuKongIM/internal/webhook"
	cluster "github.com/WuKongIM/WuKongIM/pkg/cluster/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/node/clusterconfig"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/node/types"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/store"
	"github.com/WuKongIM/WuKongIM/pkg/trace"
	"github.com/WuKongIM/WuKongIM/pkg/wkcache"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/WuKongIM/WuKongIM/version"
	"github.com/fatih/color"
	"github.com/gin-gonic/gin"
	"github.com/judwhite/go-svc"
	"go.uber.org/zap"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

type Server struct {
	opts          *options.Options // 配置
	wklog.Log                      // 日志
	clusterServer *cluster.Server  // 分布式服务实现
	ctx           context.Context
	cancel        context.CancelFunc
	start         time.Time     // 服务开始时间
	store         *store.Store  // 存储相关接口
	engine        *wknet.Engine // 长连接引擎
	// userReactor    *userReactor    // 用户的reactor，用于处理用户的行为逻辑
	trace             *trace.Trace // 监控
	demoServer        *DemoServer  // demo server
	datasource        IDatasource  // 数据源
	apiServer         *api.Server  // api服务
	ingress           *ingress.Ingress
	messageEventCache *wkcache.MessageEventCache

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

	// plugin server
	pluginServer *plugin.Server
}

func New(opts *options.Options) *Server {
	now := time.Now().UTC()

	options.G = opts

	s := &Server{
		opts:  opts,
		Log:   wklog.NewWKLog("Server"),
		start: now,
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

	s.webhook = webhook.New()
	service.Webhook = s.webhook

	s.messageEventCache = wkcache.NewMessageEventCache(nil)
	service.MessageEventCache = s.messageEventCache
	// manager
	s.retryManager = manager.NewRetryManager()                 // 消息重试管理
	s.conversationManager = manager.NewConversationManager(10) // 会话管理
	s.tagManager = manager.NewTagManager(16, func() uint64 {
		return service.Cluster.NodeVersion()
	})
	// register service
	service.ConnManager = manager.NewConnManager(18, s.engine) // 连接管理
	service.ConversationManager = s.conversationManager
	service.RetryManager = s.retryManager
	service.TagManager = s.tagManager
	service.SystemAccountManager = manager.NewSystemAccountManager()       // 系统账号管理
	service.Permission = service.NewPermissionService(ingress.NewClient()) // 权限服务

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
	role := types.NodeRole_NodeRoleReplica
	if s.opts.Cluster.Role == options.RoleProxy {
		role = types.NodeRole_NodeRoleProxy
	}
	clusterServer := cluster.New(
		cluster.NewOptions(
			cluster.WithConfigOptions(clusterconfig.NewOptions(
				clusterconfig.WithNodeId(s.opts.Cluster.NodeId),
				clusterconfig.WithConfigPath(path.Join(s.opts.DataDir, "cluster", "config", "remote.json")),
				clusterconfig.WithInitNodes(initNodes),
				clusterconfig.WithSlotCount(uint32(s.opts.Cluster.SlotCount)),
				clusterconfig.WithApiServerAddr(s.opts.Cluster.APIUrl),
				clusterconfig.WithChannelMaxReplicaCount(uint32(s.opts.Cluster.ChannelReplicaCount)),
				clusterconfig.WithSlotMaxReplicaCount(uint32(s.opts.Cluster.SlotReplicaCount)),
				clusterconfig.WithPongMaxTick(s.opts.Cluster.PongMaxTick),
				clusterconfig.WithServerAddr(s.opts.Cluster.ServerAddr),
				clusterconfig.WithSeed(s.opts.Cluster.Seed),
				clusterconfig.WithChannelDestoryAfterIdleTick(s.opts.Cluster.ChannelDestoryAfterIdleTick),
				clusterconfig.WithTickInterval(s.opts.Cluster.TickInterval),
			)),
			cluster.WithAddr(s.opts.Cluster.Addr),
			cluster.WithDataDir(path.Join(opts.DataDir)),
			cluster.WithSeed(s.opts.Cluster.Seed),
			cluster.WithRole(role),
			cluster.WithServerAddr(s.opts.Cluster.ServerAddr),
			cluster.WithDBSlotShardNum(s.opts.Db.SlotShardNum),
			cluster.WithDBSlotMemTableSize(s.opts.Db.MemTableSize),
			cluster.WithDBWKDbShardNum(s.opts.Db.ShardNum),
			cluster.WithDBWKDbMemTableSize(s.opts.Db.MemTableSize),
			cluster.WithAuth(s.opts.Auth),
			cluster.WithIsCmdChannel(s.opts.IsCmdChannel),
			cluster.WithAppVersion(version.Version),
		),

		// cluster.WithOnChannelMetaApply(func(channelID string, channelType uint8, logs []replica.Log) error {
		// 	return s.store.OnMetaApply(channelID, channelType, logs)
		// }),
	)
	service.Cluster = clusterServer
	s.clusterServer = clusterServer

	service.Store = clusterServer.GetStore()

	clusterServer.OnMessage(func(fromNodeId uint64, msg *proto.Message) {
		s.handleClusterMessage(fromNodeId, msg)
	})

	s.apiServer = api.New()

	// plugin server
	s.pluginServer = plugin.NewServer(
		plugin.NewOptions(
			plugin.WithDir(path.Join(s.opts.RootDir, "plugins")),
			plugin.WithSocketPath(s.opts.Plugin.SocketPath),
			plugin.WithInstall(s.opts.Plugin.Install),
		),
	)
	service.PluginManager = s.pluginServer
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
	// 显示增强的启动横幅
	s.printEnhancedBanner()

	startTime := time.Now()

	defer func() {
		duration := time.Since(startTime)
		s.Info(fmt.Sprintf("🚀 Server is ready! (startup time: %v)", duration))
	}()

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

	err = s.clusterServer.Start()
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

	err = s.pluginServer.Start()
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

	s.pluginServer.Stop()

	s.userEventPool.Stop()

	s.channelEventPool.Stop()

	s.pushEventPool.Stop()

	s.apiServer.Stop()

	s.retryManager.Stop()

	s.commonService.Stop()

	if s.messageEventCache != nil {
		s.messageEventCache.Close()
		s.Debug("MessageEventCache closed")
	}

	if s.opts.Conversation.On {
		s.conversationManager.Stop()
	}

	s.clusterServer.Stop()

	if s.opts.Demo.On {
		s.demoServer.Stop()
	}

	err := s.engine.Stop()
	if err != nil {
		s.Error("engine stop error", zap.Error(err))
	}
	s.trace.Stop()

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
func (s *Server) GetClusterConfig() *types.Config {
	return s.clusterServer.GetConfigServer().GetClusterConfig()
}

// 迁移槽
func (s *Server) MigrateSlot(slotId uint32, fromNodeId, toNodeId uint64) error {

	return nil
	// return s.clusterServer.MigrateSlot(slotId, fromNodeId, toNodeId)
}

func (s *Server) getSlotId(v string) uint32 {
	return service.Cluster.GetSlotId(v)
}

// printEnhancedBanner 打印增强的启动横幅
func (s *Server) printEnhancedBanner() {
	// 定义颜色
	cyan := color.New(color.FgCyan, color.Bold)
	yellow := color.New(color.FgYellow, color.Bold)
	green := color.New(color.FgGreen, color.Bold)
	blue := color.New(color.FgBlue, color.Bold)
	magenta := color.New(color.FgMagenta, color.Bold)
	white := color.New(color.FgWhite, color.Bold)

	// 打印空行
	fmt.Println()

	// 打印 ASCII 艺术字
	cyan.Println("    ╔══════════════════════════════════════════════════════════════════╗")
	cyan.Println("    ║                                                                  ║")
	cyan.Print("    ║  ")
	yellow.Print("🐒 ")
	magenta.Print("██╗    ██╗██╗   ██╗██╗  ██╗ ██████╗ ███╗   ██╗ ██████╗ ██╗███╗   ███╗")
	cyan.Println("  ║")
	cyan.Print("    ║     ")
	magenta.Print("██║    ██║██║   ██║██║ ██╔╝██╔═══██╗████╗  ██║██╔════╝ ██║████╗ ████║")
	cyan.Println("  ║")
	cyan.Print("    ║     ")
	magenta.Print("██║ █╗ ██║██║   ██║█████╔╝ ██║   ██║██╔██╗ ██║██║  ███╗██║██╔████╔██║")
	cyan.Println("  ║")
	cyan.Print("    ║     ")
	magenta.Print("██║███╗██║██║   ██║██╔═██╗ ██║   ██║██║╚██╗██║██║   ██║██║██║╚██╔╝██║")
	cyan.Println("  ║")
	cyan.Print("    ║     ")
	magenta.Print("╚███╔███╔╝╚██████╔╝██║  ██╗╚██████╔╝██║ ╚████║╚██████╔╝██║██║ ╚═╝ ██║")
	cyan.Println("  ║")
	cyan.Print("    ║      ")
	magenta.Print("╚══╝╚══╝  ╚═════╝ ╚═╝  ╚═╝ ╚═════╝ ╚═╝  ╚═══╝ ╚═════╝ ╚═╝╚═╝     ╚═╝")
	cyan.Println("  ║")
	cyan.Println("    ║                                                                  ║")
	cyan.Print("    ║           ")
	white.Print("High-Performance Instant Messaging System")
	cyan.Println("                ║")
	cyan.Println("    ║                                                                  ║")
	cyan.Println("    ╚══════════════════════════════════════════════════════════════════╝")

	fmt.Println()

	// 系统信息
	green.Print("🚀 Starting WuKongIM Server...")
	fmt.Println()
	fmt.Println()

	// 配置信息
	blue.Print("📋 Configuration:")
	fmt.Println()
	fmt.Printf("   ├─ Config File: %s\n", s.opts.ConfigFileUsed())
	fmt.Printf("   ├─ Mode: %s\n", s.getModeWithIcon())
	fmt.Printf("   ├─ Version: %s\n", version.Version)
	fmt.Printf("   ├─ Git: %s-%s\n", version.CommitDate, version.Commit)
	fmt.Printf("   ├─ Go Build: %s\n", runtime.Version())
	fmt.Printf("   └─ Data Directory: %s\n", s.opts.DataDir)

	fmt.Println()

	// 网络监听信息
	yellow.Print("🌐 Network Endpoints:")
	fmt.Println()
	fmt.Printf("   ├─ TCP Client: %s\n", s.opts.Addr)
	fmt.Printf("   ├─ WebSocket: %s\n", s.opts.WSAddr)
	if s.opts.WSSAddr != "" {
		fmt.Printf("   ├─ WebSocket Secure: %s\n", s.opts.WSSAddr)
	}
	fmt.Printf("   ├─ HTTP API: http://%s\n", s.opts.HTTPAddr)

	// 文档端点信息（根据模式显示）
	if s.opts.Mode != options.ReleaseMode {
		green.Printf("   ├─ 📚 API Documentation: http://%s/docs\n", s.opts.HTTPAddr)
	}

	if s.opts.Manager.On {
		fmt.Printf("   ├─ Manager: %s\n", s.opts.Manager.Addr)
	}

	if s.opts.Demo.On {
		fmt.Printf("   └─ 🎮 Demo: http://%s\n", s.opts.Demo.Addr)
	} else {
		fmt.Printf("   └─ Demo: disabled\n")
	}

	fmt.Println()

	// 功能状态
	magenta.Print("⚙️  Features:")
	fmt.Println()
	fmt.Printf("   ├─ Cluster Mode: %s\n", s.getClusterStatus())
	fmt.Printf("   ├─ Conversation: %s\n", s.getBoolStatus(s.opts.Conversation.On))
	fmt.Printf("   ├─ Token Auth: %s\n", s.getBoolStatus(s.opts.TokenAuthOn))
	fmt.Printf("   ├─ Encryption: %s\n", s.getBoolStatus(!s.opts.DisableEncryption))
	fmt.Printf("   └─ Documentation: %s\n", s.getDocsStatus())

	fmt.Println()

	// 启动提示
	white.Print("💡 Quick Links:")
	fmt.Println()
	fmt.Printf("   ├─ Health Check: http://%s/health\n", s.opts.HTTPAddr)
	if s.opts.Mode != options.ReleaseMode {
		fmt.Printf("   ├─ API Docs: http://%s/docs\n", s.opts.HTTPAddr)
	}
	if s.opts.Demo.On {
		fmt.Printf("   ├─ Chat Demo: http://%s\n", s.opts.Demo.Addr)
	}
	fmt.Printf("   └─ System Info: http://%s/varz\n", s.opts.HTTPAddr)

	fmt.Println()
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println()
}

// getModeWithIcon 获取带图标的模式显示
func (s *Server) getModeWithIcon() string {
	switch s.opts.Mode {
	case options.ReleaseMode:
		return "🚀 release (production)"
	case options.DebugMode:
		return "🐛 debug (development)"
	case options.BenchMode:
		return "⚡ bench (performance testing)"
	default:
		return fmt.Sprintf("❓ %s", s.opts.Mode)
	}
}

// getBoolStatus 获取布尔状态的显示
func (s *Server) getBoolStatus(enabled bool) string {
	if enabled {
		return "✅ enabled"
	}
	return "❌ disabled"
}

// getClusterStatus 获取集群状态
func (s *Server) getClusterStatus() string {
	if len(s.opts.Cluster.InitNodes) > 0 {
		return fmt.Sprintf("✅ enabled (%d nodes)", len(s.opts.Cluster.InitNodes))
	}
	return "❌ standalone mode"
}

// getDocsStatus 获取文档服务状态
func (s *Server) getDocsStatus() string {
	if s.opts.Mode == options.ReleaseMode {
		return "🔒 disabled (release mode)"
	}
	return "📚 enabled (development mode)"
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
		userLeaderId := s.userLeaderId(connCtx.Uid)

		// 如果当前连接即属于本节点，本节点又是此连接的领导节点,则发起移除事件
		if options.G.IsLocalNode(userLeaderId) && userLeaderId == connCtx.NodeId {
			eventbus.User.RemoveConn(connCtx)
		} else if !options.G.IsLocalNode(userLeaderId) {
			// 直接移除连接（不会触发移除事件）
			eventbus.User.DirectRemoveConn(connCtx)
			// 如果当前节点不是用户的领导节点，则通知领导节点移除连接
			eventbus.User.RemoveLeaderConn(connCtx)
		} else {
			// 如果两个都不是，仅仅移除本地的连接
			eventbus.User.DirectRemoveConn(connCtx)
		}

	}
	// 移除连接管理的此连接
	service.ConnManager.RemoveConn(conn)
}

// 获取用户领导id
func (s *Server) userLeaderId(uid string) uint64 {
	slotId := service.Cluster.GetSlotId(uid)
	leaderId := service.Cluster.SlotLeaderId(slotId)
	return leaderId
}

func (s *Server) WithRequestTimeout() (context.Context, context.CancelFunc) {
	return context.WithTimeout(s.ctx, s.opts.Cluster.ReqTimeout)
}
