package server

import (
	"context"
	"fmt"
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
	"github.com/WuKongIM/WuKongIM/pkg/trace"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"github.com/WuKongIM/WuKongIM/version"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/gin-gonic/gin"
	"github.com/judwhite/go-svc"
	"github.com/pkg/errors"
	"go.etcd.io/etcd/pkg/v3/idutil"
	"go.uber.org/zap"
)

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

	// 初始化监控追踪
	s.trace = trace.New(
		s.ctx,
		trace.NewOptions(
			trace.WithEndpoint(s.opts.Trace.Endpoint),
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
	s.store = clusterstore.NewStore(storeOpts)

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
			cluster.WithSlotDbShardNum(s.opts.Db.ShardNum),
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
	err = s.tagManager.start()
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

	s.conversationManager.Start()

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
	s.conversationManager.Stop()
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

	s.tagManager.stop()

	s.timingWheel.Stop()

	s.tagManager.stop()

	s.Info("Server is stopped")

	return nil
}

// 等待分布式就绪
func (s *Server) MustWaitClusterReady() {
	s.cluster.MustWaitClusterReady()
}

// 提案频道分布式
func (s *Server) ProposeChannelClusterConfig(ctx context.Context, cfg wkdb.ChannelClusterConfig) error {
	return s.clusterServer.ProposeChannelClusterConfig(ctx, cfg)
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

func (s *Server) onData(conn wknet.Conn) error {
	buff, err := conn.Peek(-1)
	if err != nil {
		return err
	}
	if len(buff) == 0 {
		return nil
	}
	data, _ := gnetUnpacket(buff)
	if len(data) == 0 {
		return nil
	}

	var isAuth bool
	var connCtx *connContext
	connCtxObj := conn.Context()
	if connCtxObj != nil {
		connCtx = connCtxObj.(*connContext)
		isAuth = connCtx.isAuth.Load()
	} else {
		isAuth = false
	}

	if !isAuth {
		packet, _, err := s.opts.Proto.DecodeFrame(data, wkproto.LatestVersion)
		if err != nil {
			s.Warn("Failed to decode the message,conn will be closed", zap.Error(err))
			conn.Close()
			return nil
		}
		if packet == nil {
			s.Warn("packet is nil,conn will be closed", zap.ByteString("data", data))
			conn.Close()
			return nil
		}
		if packet.GetFrameType() != wkproto.CONNECT {
			s.Warn("请先进行连接！")
			conn.Close()
			return nil
		}
		connectPacket := packet.(*wkproto.ConnectPacket)

		if strings.TrimSpace(connectPacket.UID) == "" {
			s.Warn("UID is empty,conn will be closed")
			conn.Close()
			return nil
		}
		if IsSpecialChar(connectPacket.UID) {
			s.Warn("UID is illegal,conn will be closed", zap.String("uid", connectPacket.UID))
			conn.Close()
			return nil
		}

		sub := s.userReactor.reactorSub(connectPacket.UID)
		connInfo := connInfo{
			connId:       conn.ID(),
			uid:          connectPacket.UID,
			deviceId:     connectPacket.DeviceID,
			deviceFlag:   wkproto.DeviceFlag(connectPacket.DeviceFlag),
			protoVersion: connectPacket.Version,
		}
		connCtx = newConnContext(connInfo, conn, sub)
		conn.SetContext(connCtx)

		s.userReactor.addConnContext(connCtx)

		connCtx.addConnectPacket(connectPacket)

		//  process conn auth
		_, _ = conn.Discard(len(data))
		// s.processAuth(conn, packet.(*wkproto.ConnectPacket))
	} else {
		offset := 0
		for len(data) > offset {
			frame, size, err := s.opts.Proto.DecodeFrame(data[offset:], connCtx.protoVersion)
			if err != nil { //
				s.Warn("Failed to decode the message", zap.Error(err))
				conn.Close()
				return err
			}
			if frame == nil {
				break
			}
			offset += size
			if frame.GetFrameType() == wkproto.SEND {
				connCtx.addSendPacket(frame.(*wkproto.SendPacket))
			} else {
				connCtx.addOtherPacket(frame)
			}
		}
		_, _ = conn.Discard(offset)
	}

	return nil
}

func (s *Server) onConnect(conn wknet.Conn) error {
	conn.SetMaxIdle(time.Second * 2) // 在认证之前，连接最多空闲2秒
	s.trace.Metrics.App().ConnCountAdd(1)
	return nil
}

func (s *Server) onClose(conn wknet.Conn) {
	s.trace.Metrics.App().ConnCountAdd(-1)
	connCtxObj := conn.Context()
	if connCtxObj != nil {
		connCtx := connCtxObj.(*connContext)
		s.userReactor.removeConnContextById(connCtx.uid, connCtx.connId)

		if connCtx.isAuth.Load() {
			deviceOnlineCount := s.userReactor.getConnContextCountByDeviceFlag(connCtx.uid, connCtx.deviceFlag)
			totalOnlineCount := s.userReactor.getConnContextCount(connCtx.uid)
			s.webhook.Offline(connCtx.uid, wkproto.DeviceFlag(connCtx.deviceFlag), connCtx.connId, deviceOnlineCount, totalOnlineCount) // 触发离线webhook

			s.trace.Metrics.App().OnlineDeviceCountAdd(-1)
		}

	}
}

func gnetUnpacket(buff []byte) ([]byte, error) {
	// buff, _ := c.Peek(-1)
	if len(buff) <= 0 {
		return nil, nil
	}
	offset := 0

	for len(buff) > offset {
		typeAndFlags := buff[offset]
		packetType := wkproto.FrameType(typeAndFlags >> 4)
		if packetType == wkproto.PING || packetType == wkproto.PONG {
			offset++
			continue
		}
		reminLen, readSize, has := decodeLength(buff[offset+1:])
		if !has {
			break
		}
		dataEnd := offset + readSize + reminLen + 1
		if len(buff) >= dataEnd { // 总数据长度大于当前包数据长度 说明还有包可读。
			offset = dataEnd
			continue
		} else {
			break
		}
	}

	if offset > 0 {
		return buff[:offset], nil
	}

	return nil, nil
}

func decodeLength(data []byte) (int, int, bool) {
	var rLength uint32
	var multiplier uint32
	offset := 0
	for multiplier < 27 { //fix: Infinite '(digit & 128) == 1' will cause the dead loop
		if offset >= len(data) {
			return 0, 0, false
		}
		digit := data[offset]
		offset++
		rLength |= uint32(digit&127) << multiplier
		if (digit & 128) == 0 {
			break
		}
		multiplier += 7
	}
	return int(rLength), offset, true
}

// func (s *Server) response(connCtx *connContext, packet wkproto.Frame) {
// 	err := s.userReactor.writePacket(connCtx, packet)
// 	if err != nil {
// 		s.Error("write packet error", zap.Error(err))
// 	}
// }

// func (s *Server) responseData(conn wknet.Conn, data []byte) {
// 	s.responseDataNoFlush(conn, data)
// 	s.flushConnData(conn)
// }

// func (s *Server) responseDataNoFlush(conn wknet.Conn, data []byte) {
// 	wsConn, wsok := conn.(wknet.IWSConn) // websocket连接
// 	if wsok {
// 		err := wsConn.WriteServerBinary(data)
// 		if err != nil {
// 			s.Warn("Failed to write the message", zap.Error(err))
// 		}

// 	} else {
// 		_, err := conn.WriteToOutboundBuffer(data)
// 		if err != nil {
// 			s.Warn("Failed to write the message", zap.Error(err))
// 		}
// 	}
// }

// func (s *Server) flushConnData(conn wknet.Conn) {
// 	err := conn.WakeWrite()
// 	if err != nil {
// 		s.Warn("Failed to wake write", zap.Error(err), zap.String("uid", conn.UID()), zap.Int("fd", conn.Fd().Fd()), zap.String("deviceId", conn.DeviceID()))
// 	}
// }

// func (s *Server) responseConnackAuthFail(c *connContext) {
// 	s.responseConnack(c, 0, wkproto.ReasonAuthFail)
// }

// func (s *Server) responseConnack(c *connContext, timeDiff int64, code wkproto.ReasonCode) {

// 	s.response(c, &wkproto.ConnackPacket{
// 		ReasonCode: code,
// 		TimeDiff:   timeDiff,
// 	})
// }

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
	decodePayload, err := wkutil.AesDecryptPkcs7Base64(sendPacket.Payload, []byte(aesKey), []byte(aesIV))
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
	actMsgKey, err := wkutil.AesEncryptPkcs7Base64([]byte(signStr), []byte(aesKey), []byte(aesIV))
	if err != nil {
		s.Error("msgKey is illegal！", zap.Error(err), zap.String("sign", signStr), zap.String("aesKey", aesKey), zap.String("aesIV", aesIV), zap.Any("conn", conn))
		return false, err
	}
	actMsgKeyStr := sendPacket.MsgKey
	exceptMsgKey := wkutil.MD5(string(actMsgKey))
	if actMsgKeyStr != exceptMsgKey {
		s.Error("msgKey is illegal！", zap.String("except", exceptMsgKey), zap.String("act", actMsgKeyStr), zap.String("sign", signStr), zap.String("aesKey", aesKey), zap.String("aesIV", aesIV), zap.Any("conn", conn))
		return false, errors.New("msgKey is illegal！")
	}
	return true, nil
}
