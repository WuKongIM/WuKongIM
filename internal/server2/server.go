package server

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"github.com/RussellLuo/timingwheel"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/clusterconfig/pb"
	cluster "github.com/WuKongIM/WuKongIM/pkg/cluster/clusterserver"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/clusterstore"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/icluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/trace"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/WuKongIM/WuKongIM/version"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/gin-gonic/gin"
	"github.com/judwhite/go-svc"
	"github.com/panjf2000/ants/v2"
	"go.etcd.io/etcd/pkg/v3/idutil"
	"go.uber.org/zap"
)

type Server struct {
	opts        *Options          // 配置
	wklog.Log                     // 日志
	cluster     icluster.Cluster  // 分布式
	reqIDGen    *idutil.Generator // 请求ID生成器
	ctx         context.Context
	timingWheel *timingwheel.TimingWheel // Time wheel delay task
	start       time.Time                // 服务开始时间
	store       *clusterstore.Store      // 存储相关接口
	engine      *wknet.Engine            // 长连接引擎
	authPool    *ants.Pool               // 认证的协程池

	userReactor    *userReactor    // 用户的reactor
	channelReactor *channelReactor // 频道的reactor
	webhook        *webhook        // webhook
	trace          *trace.Trace    // 监控

	demoServer *DemoServer // demo server
	apiServer  *APIServer  // api服务

	systemUIDManager *SystemUIDManager
}

func New(opts *Options) *Server {
	now := time.Now().UTC()
	s := &Server{
		opts:        opts,
		Log:         wklog.NewWKLog("Server"),
		timingWheel: timingwheel.NewTimingWheel(opts.TimingWheelTick, opts.TimingWheelSize),
		reqIDGen:    idutil.NewGenerator(uint16(opts.Cluster.NodeId), time.Now()),
		ctx:         context.Background(),
		start:       now,
	}

	s.trace = trace.New(
		s.ctx,
		trace.NewOptions(
			trace.WithEndpoint(s.opts.Trace.Endpoint),
			trace.WithServiceName(s.opts.Trace.ServiceName),
			trace.WithServiceHostName(s.opts.Trace.ServiceHostName),
		))
	trace.SetGlobalTrace(s.trace)

	var err error
	s.authPool, err = ants.NewPool(opts.Process.AuthPoolSize, ants.WithPanicHandler(func(i interface{}) {
		stack := debug.Stack()
		s.Panic("authPool panic", zap.String("stack", string(stack)))
	}))
	if err != nil {
		s.Panic("create auth pool error", zap.Error(err))
	}

	gin.SetMode(opts.GinMode)

	storeOpts := clusterstore.NewOptions(s.opts.Cluster.NodeId)
	storeOpts.DataDir = path.Join(s.opts.DataDir, "db")
	storeOpts.SlotCount = uint32(s.opts.Cluster.SlotCount)
	storeOpts.GetSlotId = s.getSlotId
	s.store = clusterstore.NewStore(storeOpts)

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
	s.webhook = newWebhook(s)
	s.channelReactor = newChannelReactor(s, opts)
	s.userReactor = newUserReactor(s)
	s.demoServer = NewDemoServer(s)
	s.systemUIDManager = NewSystemUIDManager(s)
	s.apiServer = NewAPIServer(s)

	initNodes := make(map[uint64]string)

	if len(s.opts.Cluster.Nodes) > 0 {
		for _, node := range s.opts.Cluster.Nodes {
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
			cluster.WithApiServerAddr(s.opts.External.APIUrl),
			cluster.WithChannelMaxReplicaCount(s.opts.Cluster.ChannelReplicaCount),
			cluster.WithSlotMaxReplicaCount(uint32(s.opts.Cluster.SlotReplicaCount)),
			cluster.WithLogLevel(s.opts.Logger.Level),
			cluster.WithAppVersion(version.Version),
			cluster.WithDB(s.store.DB()),
			cluster.WithOnSlotApply(func(slotId uint32, logs []replica.Log) error {

				return s.store.OnMetaApply(slotId, logs)
			}),
			cluster.WithChannelClusterStorage(clusterstore.NewChannelClusterConfigStore(s.store)),
		),

		// cluster.WithOnChannelMetaApply(func(channelID string, channelType uint8, logs []replica.Log) error {
		// 	return s.store.OnMetaApply(channelID, channelType, logs)
		// }),
	)
	s.cluster = clusterServer
	storeOpts.Cluster = clusterServer

	clusterServer.OnMessage(func(msg *proto.Message) {
		s.handleClusterMessage(msg)
	})

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

	if s.opts.Monitor.On {
		s.Info(fmt.Sprintf("Listening  for Monitor on %s", s.opts.Monitor.Addr))
	}

	defer s.Info("Server is ready")

	err := s.trace.Start()
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
	return err
}

func (s *Server) Stop() error {
	s.cluster.Stop()
	s.apiServer.Stop()
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
	s.Info("Server is stopped")

	return nil
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
		isAuth = true
	} else {
		isAuth = false
	}

	if !isAuth {
		packet, _, err := s.opts.Proto.DecodeFrame(data, wkproto.LatestVersion)
		if err != nil {
			s.Warn("Failed to decode the message", zap.Error(err))
			conn.Close()
			return nil
		}
		if packet == nil {
			s.Warn("message is nil", zap.ByteString("data", data))
			return nil
		}
		if packet.GetFrameType() != wkproto.CONNECT {
			s.Warn("请先进行连接！")
			conn.Close()
			return nil
		}
		//  process conn auth
		_, _ = conn.Discard(len(data))
		s.processAuth(conn, packet.(*wkproto.ConnectPacket))
	} else {
		offset := 0
		for len(data) > offset {
			frame, size, err := s.opts.Proto.DecodeFrame(data[offset:], uint8(conn.ProtoVersion()))
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
	return nil
}

func (s *Server) onClose(conn wknet.Conn) {

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

func (s *Server) response(connCtx *connContext, packet wkproto.Frame) {
	err := s.userReactor.writePacket(connCtx, packet)
	if err != nil {
		s.Error("write packet error", zap.Error(err))
	}
}

func (s *Server) responseWithConn(conn wknet.Conn, packet wkproto.Frame) {
	data, err := s.opts.Proto.EncodeFrame(packet, uint8(conn.ProtoVersion()))
	if err != nil {
		s.Error("encode frame error", zap.Error(err))
		return
	}
	s.responseData(conn, data)
}

func (s *Server) responseData(conn wknet.Conn, data []byte) {

	wsConn, wsok := conn.(wknet.IWSConn) // websocket连接
	if wsok {
		err := wsConn.WriteServerBinary(data)
		if err != nil {
			s.Warn("Failed to write the message", zap.Error(err))
		}

	} else {
		_, err := conn.WriteToOutboundBuffer(data)
		if err != nil {
			s.Warn("Failed to write the message", zap.Error(err))
		}
	}
	err := conn.WakeWrite()
	if err != nil {
		s.Warn("Failed to wake write", zap.Error(err), zap.String("uid", conn.UID()), zap.Int("fd", conn.Fd().Fd()), zap.String("deviceId", conn.DeviceID()))
	}
}

func (s *Server) responseConnackAuthFail(c wknet.Conn) {
	s.responseConnack(c, 0, wkproto.ReasonAuthFail)
}

func (s *Server) responseConnack(c wknet.Conn, timeDiff int64, code wkproto.ReasonCode) {

	s.responseWithConn(c, &wkproto.ConnackPacket{
		ReasonCode: code,
		TimeDiff:   timeDiff,
	})
}
