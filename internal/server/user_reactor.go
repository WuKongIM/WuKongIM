package server

import (
	"context"
	"fmt"
	"hash/fnv"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/lni/goutils/syncutil"
	"github.com/panjf2000/ants/v2"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

type userReactor struct {
	processInitC    chan *userInitReq // 初始化请求
	processAuthC    chan *userAuthReq // 认证请求
	processPingC    chan *pingReq     // ping请求
	processRecvackC chan *recvackReq  // recvack请求
	processWriteC   chan *writeReq    // 写请求

	processForwardUserActionC chan UserAction           // 转发用户行为请求
	processNodePingC          chan *nodePingReq         // 节点ping请求
	processNodePongC          chan *nodePongReq         // 节点pong请求
	processProxyNodeTimeoutC  chan *proxyNodeTimeoutReq // 代理节点超时
	processCloseC             chan *userCloseReq        // 关闭请求
	processCheckLeaderC       chan *checkLeaderReq      // 检查leader请求

	processGoPool *ants.MultiPool

	stopper *syncutil.Stopper
	wklog.Log
	s    *Server
	subs []*userReactorSub

	stopped atomic.Bool
	// mu   deadlock.RWMutex
}

func newUserReactor(s *Server) *userReactor {
	u := &userReactor{
		processInitC:              make(chan *userInitReq, 1024),
		processAuthC:              make(chan *userAuthReq, 1024),
		processPingC:              make(chan *pingReq, 1024),
		processRecvackC:           make(chan *recvackReq, 1024),
		processWriteC:             make(chan *writeReq, 1024),
		processForwardUserActionC: make(chan UserAction, 1024*10),
		processNodePingC:          make(chan *nodePingReq, 1024),
		processNodePongC:          make(chan *nodePongReq, 1024),
		processProxyNodeTimeoutC:  make(chan *proxyNodeTimeoutReq, 1024),
		processCloseC:             make(chan *userCloseReq, 1024),
		processCheckLeaderC:       make(chan *checkLeaderReq, 1024),
		stopper:                   syncutil.NewStopper(),
		Log:                       wklog.NewWKLog(fmt.Sprintf("userReactor[%d]", s.opts.Cluster.NodeId)),
		s:                         s,
	}

	u.subs = make([]*userReactorSub, s.opts.Reactor.User.SubCount)
	for i := 0; i < s.opts.Reactor.User.SubCount; i++ {
		sub := newUserReactorSub(i, u)
		u.subs[i] = sub
	}

	size := 0
	sizePerPool := 0
	if s.opts.Reactor.User.ProcessPoolSize <= s.opts.Reactor.User.SubCount {
		size = 1
		sizePerPool = s.opts.Reactor.User.ProcessPoolSize
	} else {
		size = s.opts.Reactor.User.SubCount
		sizePerPool = s.opts.Reactor.User.ProcessPoolSize / s.opts.Reactor.User.SubCount
	}
	var err error
	u.processGoPool, err = ants.NewMultiPool(size, sizePerPool, ants.LeastTasks, ants.WithPanicHandler(func(i interface{}) {
		u.Panic("user: processGoPool panic", zap.Any("panic", i), zap.Stack("stack"))
	}))
	if err != nil {
		u.Panic("user: NewMultiPool panic", zap.Error(err))
	}
	return u
}

func (u *userReactor) start() error {

	// 高并发处理，适用于分散的耗时任务
	for i := 0; i < 100; i++ {

	}

	// 中并发处理，适合于分散但是不是很耗时的任务
	for i := 0; i < 50; i++ {
		u.stopper.RunWorker(u.processWriteLoop)
		u.stopper.RunWorker(u.processForwardUserActionLoop)

	}

	// 低并发处理，适合于集中的耗时任务，这样可以合并请求批量处理
	for i := 0; i < 1; i++ {
		u.stopper.RunWorker(u.processInitLoop)
		u.stopper.RunWorker(u.processProxyNodeTimeoutLoop)
		u.stopper.RunWorker(u.processCloseLoop)

		u.stopper.RunWorker(u.processNodePongLoop)

		u.stopper.RunWorker(u.processRecvackLoop)
		u.stopper.RunWorker(u.processCheckLeaderLoop)

		u.stopper.RunWorker(u.processNodePingLoop)
		u.stopper.RunWorker(u.processPingLoop)
		u.stopper.RunWorker(u.processAuthLoop)

	}

	for _, sub := range u.subs {
		err := sub.start()
		if err != nil {
			return err
		}
	}

	return nil
}

func (u *userReactor) stop() {

	u.Info("UserReactor stop")
	u.stopped.Store(true)
	u.stopper.Stop()

	for _, sub := range u.subs {
		sub.stop()
	}
}

func (u *userReactor) reactorSub(uid string) *userReactorSub {

	h := fnv.New32a()
	h.Write([]byte(uid))

	i := h.Sum32() % uint32(len(u.subs))
	return u.subs[i]
}

// 获取用户处理者
func (u *userReactor) getUserHandler(uid string) *userHandler {
	sub := u.reactorSub(uid)
	return sub.getUserHandler(uid)
}

// 获取指定设备标识的连接
func (u *userReactor) getConnsByDeviceFlag(uid string, deviceFlag wkproto.DeviceFlag) []*connContext {
	return u.reactorSub(uid).getConnsByDeviceFlag(uid, deviceFlag)
}

func (u *userReactor) getConnCountByDeviceFlag(uid string, deviceFlag wkproto.DeviceFlag) int {
	return u.reactorSub(uid).getConnCountByDeviceFlag(uid, deviceFlag)
}

func (u *userReactor) getConnCount(uid string) int {
	return len(u.getConns(uid))
}

// 获取用户的所有连接
func (u *userReactor) getConns(uid string) []*connContext {
	userHandler := u.reactorSub(uid).getUserHandler(uid)
	if userHandler == nil {
		return nil
	}
	return userHandler.getConns()
}

// 获取指定用户的指定id的连接
func (u *userReactor) getConnById(uid string, id int64) *connContext {
	userHandler := u.reactorSub(uid).getUserHandler(uid)
	if userHandler == nil {
		return nil
	}
	return userHandler.getConnById(id)
}

// 添加连接如果不存在用户处理者则创建用户处理者后再添加连接
func (u *userReactor) addConnAndCreateUserHandlerIfNotExist(conn *connContext) {
	sub := u.reactorSub(conn.uid)
	sub.addConnAndCreateUserHandlerIfNotExist(conn)
}

// 移除指定用户的指定id的连接
func (u *userReactor) removeConnById(uid string, id int64) {
	sub := u.reactorSub(uid)
	sub.removeConnById(uid, id)
}

func (u *userReactor) removeConnsByNodeId(uid string, nodeId uint64) []*connContext {
	sub := u.reactorSub(uid)
	return sub.removeConnsByNodeId(uid, nodeId)
}

func (u *userReactor) removeUserHandler(uid string) {
	sub := u.reactorSub(uid)
	sub.removeUserHandler(uid)
}

func (u *userReactor) getHandlerCount() int {
	count := 0
	for _, sub := range u.subs {
		count += sub.getHandlerCount()
	}
	return count
}

func (u *userReactor) getAllConnCount() int {
	count := 0
	for _, sub := range u.subs {
		count += sub.getAllConnCount()
	}
	return count
}

// func (u *userReactor) step(uid string, a UserAction) {
// 	u.reactorSub(uid).step(uid, a)
// }

func (u *userReactor) writePacket(conn *connContext, packet wkproto.Frame) error {
	return conn.writePacket(packet)
}

// func (u *userReactor) writePacketByDeviceId(uid string, deviceId string, packet wkproto.Frame) error {
// 	conn := u.getConnContext(uid, deviceId)
// 	if conn == nil {
// 		u.Error("conn not found", zap.String("uid", uid), zap.String("deviceId", deviceId))
// 		return ErrConnNotFound
// 	}
// 	return u.reactorSub(uid).writePacket(conn, packet)
// }

func (u *userReactor) writePacketByConnId(uid string, connId int64, packet wkproto.Frame) error {
	conn := u.getConnById(uid, connId)
	if conn == nil {
		u.Error("conn not found", zap.String("uid", uid), zap.Int64("connId", connId), zap.String("frameType", packet.GetFrameType().String()))
		return ErrConnNotFound
	}
	return u.reactorSub(uid).writePacket(conn, packet)
}

func (u *userReactor) WithTimeout() (context.Context, context.CancelFunc) {
	return context.WithTimeout(u.s.ctx, time.Second*10)
}
