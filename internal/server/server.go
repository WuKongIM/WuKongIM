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

	"github.com/RussellLuo/timingwheel"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/clusterconfig/pb"
	cluster "github.com/WuKongIM/WuKongIM/pkg/cluster/clusterserver"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/clusterstore"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/icluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/promtail"
	"github.com/WuKongIM/WuKongIM/pkg/trace"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"github.com/WuKongIM/WuKongIM/version"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/gin-gonic/gin"
	"github.com/judwhite/go-svc"
	"github.com/pkg/errors"
	"github.com/valyala/bytebufferpool"
	"go.etcd.io/etcd/pkg/v3/idutil"
	"go.uber.org/zap"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

type Server struct {
	opts          *Options          // 配置
	wklog.Log                       // 日志
	cluster       icluster.Cluster  // 分布式接口
	clusterServer *cluster.Server   // 分布式服务实现
	reqIDGen      *idutil.Generator // 请求ID生成器
	ctx           context.Context
	cancel        context.CancelFunc
	timingWheel   *timingwheel.TimingWheel // Time wheel delay task
	start         time.Time                // 服务开始时间
	store         *clusterstore.Store      // 存储相关接口
	engine        *wknet.Engine            // 长连接引擎

	userReactor    *userReactor    // 用户的reactor，用于处理用户的行为逻辑
	channelReactor *channelReactor // 频道的reactor，用户处理频道的行为逻辑
	webhook        *webhook        // webhook
	trace          *trace.Trace    // 监控

	demoServer    *DemoServer    // demo server
	apiServer     *APIServer     // api服务
	managerServer *ManagerServer // 管理者api服务

	systemUIDManager *SystemUIDManager // 系统账号管理

	tagManager     *tagManager     // tag管理，用来管理频道订阅者的tag，用于快速查找订阅者所在节点
	deliverManager *deliverManager // 消息投递管理
	retryManager   *retryManager   // 消息重试管理

	conversationManager *ConversationManager // 会话管理

	migrateTask *MigrateTask // 迁移任务

	datasource IDatasource // 数据源

	promtailServer *promtail.Promtail // 日志收集, 负责收集WuKongIM的日志 上报给Loki

}

func New(opts *Options) *Server {
	now := time.Now().UTC()
	s := &Server{
		opts:        opts,
		Log:         wklog.NewWKLog("Server"),
		timingWheel: timingwheel.NewTimingWheel(opts.TimingWheelTick, opts.TimingWheelSize),
		reqIDGen:    idutil.NewGenerator(uint16(opts.Cluster.NodeId), time.Now()),
		start:       now,
	}

	// 配置检查
	err := opts.Check()
	if err != nil {
		s.Panic("config check error", zap.Error(err))
	}

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

	// 数据源
	s.datasource = NewDatasource(s)
	// 初始化tag管理
	s.tagManager = newTagManager(s)

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
	s.webhook = newWebhook(s)                         // webhook
	s.channelReactor = newChannelReactor(s, opts)     // 频道的reactor
	s.userReactor = newUserReactor(s)                 // 用户的reactor
	s.demoServer = NewDemoServer(s)                   // demo server
	s.systemUIDManager = NewSystemUIDManager(s)       // 系统账号管理
	s.apiServer = NewAPIServer(s)                     // api服务
	s.managerServer = NewManagerServer(s)             // 管理者的api服务
	s.retryManager = newRetryManager(s)               // 消息重试管理
	s.conversationManager = NewConversationManager(s) // 会话管理
	s.migrateTask = NewMigrateTask(s)                 // 迁移任务

	// 初始化分布式服务
	initNodes := make(map[uint64]string)
	if len(s.opts.Cluster.InitNodes) > 0 {
		for _, node := range s.opts.Cluster.InitNodes {
			serverAddr := strings.ReplaceAll(node.ServerAddr, "tcp://", "")
			initNodes[node.Id] = serverAddr
		}
	}
	role := pb.NodeRole_NodeRoleReplica
	if s.opts.Cluster.Role == RoleProxy {
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
	s.cluster = clusterServer
	s.clusterServer = clusterServer
	storeOpts.Cluster = clusterServer

	clusterServer.OnMessage(func(fromNodeId uint64, msg *proto.Message) {
		s.handleClusterMessage(fromNodeId, msg)
	})

	// 消息投递管理者
	s.deliverManager = newDeliverManager(s)

	// 日志收集
	if s.opts.LokiOn() {
		s.Info("Loki is on")
		s.promtailServer = promtail.New(&promtail.Options{
			NodeId:  s.opts.Cluster.NodeId,
			Url:     s.opts.Logger.Loki.Url,
			LogDir:  s.opts.Logger.Dir,
			Address: s.opts.External.APIUrl,
			Job:     s.opts.Logger.Loki.Job,
		})
	}

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

	s.timingWheel.Start()

	err := s.tagManager.start()
	if err != nil {
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

	s.setClusterRoutes()
	err = s.cluster.Start()
	if err != nil {
		return err
	}

	s.apiServer.Start()

	s.managerServer.Start()

	err = s.channelReactor.start()
	if err != nil {
		return err
	}

	err = s.userReactor.start()
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

	err = s.deliverManager.start()
	if err != nil {
		return err
	}

	err = s.retryManager.start()
	if err != nil {
		return err
	}

	if s.opts.Conversation.On {
		err = s.conversationManager.Start()
		if err != nil {
			return err
		}
	}

	s.webhook.Start()

	// 判断是否开启迁移任务
	if strings.TrimSpace(s.opts.OldV1Api) != "" {
		s.migrateTask.Run()
	}

	if s.opts.LokiOn() {
		err = s.promtailServer.Start()
		if err != nil {
			return err
		}
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

	s.deliverManager.stop()

	s.retryManager.stop()

	if s.opts.Conversation.On {
		s.conversationManager.Stop()
	}

	s.cluster.Stop()
	s.apiServer.Stop()

	_ = s.managerServer.Stop()

	if s.opts.Demo.On {
		s.demoServer.Stop()
	}
	s.channelReactor.stop()
	s.userReactor.stop()

	err := s.engine.Stop()
	if err != nil {
		s.Error("engine stop error", zap.Error(err))
	}
	s.trace.Stop()

	s.store.Close()

	s.timingWheel.Stop()

	s.tagManager.stop()

	s.webhook.Stop()

	if s.opts.LokiOn() {
		s.promtailServer.Stop()
	}

	s.Info("Server is stopped")

	return nil
}

// 等待分布式就绪
func (s *Server) MustWaitClusterReady(timeout time.Duration) {
	s.cluster.MustWaitClusterReady(timeout)
}

func (s *Server) MustWaitAllSlotsReady(timeout time.Duration) {
	s.cluster.MustWaitAllSlotsReady(timeout)
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
	return s.cluster.GetSlotId(v)
}

func (s *Server) onConnect(conn wknet.Conn) error {
	conn.SetMaxIdle(time.Second * 2) // 在认证之前，连接最多空闲2秒
	s.trace.Metrics.App().ConnCountAdd(1)

	// if conn.InboundBuffer().BoundBufferSize() == 0 {
	// 	conn.SetValue(ConnKeyParseProxyProto, true) // 设置需要解析代理协议
	// 	return nil
	// }
	// // 解析代理协议，获取真实IP
	// buff, err := conn.Peek(-1)
	// if err != nil {
	// 	return err
	// }
	// remoteAddr, size, err := parseProxyProto(buff)
	// if err != nil && err != ErrNoProxyProtocol {
	// 	s.Warn("Failed to parse proxy proto", zap.Error(err))
	// }
	// if remoteAddr != nil {
	// 	conn.SetRemoteAddr(remoteAddr)
	// 	s.Debug("parse proxy proto success", zap.String("remoteAddr", remoteAddr.String()))
	// }
	// if size > 0 {
	// 	_, _ = conn.Discard(size)
	// }
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
		connCtx := connCtxObj.(*connContext)
		s.userReactor.removeConnById(connCtx.uid, connCtx.connId)

		if connCtx.isAuth.Load() {
			deviceOnlineCount := s.userReactor.getConnCountByDeviceFlag(connCtx.uid, connCtx.deviceFlag)
			totalOnlineCount := s.userReactor.getConnCount(connCtx.uid)
			s.webhook.Offline(connCtx.uid, wkproto.DeviceFlag(connCtx.deviceFlag), connCtx.connId, deviceOnlineCount, totalOnlineCount) // 触发离线webhook
		}

	}
}

// 代理节点关闭
func (s *Server) onCloseForProxy(conn *connContext) {

}

// Schedule 延迟任务
func (s *Server) Schedule(interval time.Duration, f func()) *timingwheel.Timer {
	return s.timingWheel.ScheduleFunc(&everyScheduler{
		Interval: interval,
	}, f)
}

// decode payload
func (s *Server) checkAndDecodePayload(sendPacket *wkproto.SendPacket, conn *connContext) ([]byte, error) {

	aesKey, aesIV := conn.aesKey, conn.aesIV
	vail, err := s.sendPacketIsVail(sendPacket, conn)
	if err != nil {
		return nil, err
	}
	if !vail {
		return nil, errors.New("sendPacket is illegal！")
	}
	// decode payload
	decodePayload, err := wkutil.AesDecryptPkcs7Base64(sendPacket.Payload, aesKey, aesIV)
	if err != nil {
		s.Error("Failed to decode payload！", zap.Error(err))
		return nil, err
	}

	return decodePayload, nil
}

// send packet is vail
func (s *Server) sendPacketIsVail(sendPacket *wkproto.SendPacket, conn *connContext) (bool, error) {
	aesKey, aesIV := conn.aesKey, conn.aesIV
	signStr := sendPacket.VerityString()

	signBuff := bytebufferpool.Get()
	_, _ = signBuff.WriteString(signStr)

	defer bytebufferpool.Put(signBuff)

	actMsgKey, err := wkutil.AesEncryptPkcs7Base64(signBuff.Bytes(), aesKey, aesIV)
	if err != nil {
		s.Error("msgKey is illegal！", zap.Error(err), zap.String("sign", signStr), zap.String("aesKey", string(aesKey)), zap.String("aesIV", string(aesIV)), zap.Any("conn", conn))
		return false, err
	}
	actMsgKeyStr := sendPacket.MsgKey
	exceptMsgKey := wkutil.MD5Bytes(actMsgKey)
	if actMsgKeyStr != exceptMsgKey {
		s.Error("msgKey is illegal！", zap.String("except", exceptMsgKey), zap.String("act", actMsgKeyStr), zap.String("sign", signStr), zap.String("aesKey", string(aesKey)), zap.String("aesIV", string(aesIV)), zap.Any("conn", conn))
		return false, errors.New("msgKey is illegal！")
	}
	return true, nil
}

func (s *Server) WithRequestTimeout() (context.Context, context.CancelFunc) {
	return context.WithTimeout(s.ctx, s.opts.Cluster.ReqTimeout)
}
