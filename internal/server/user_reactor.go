package server

import (
	"fmt"
	"hash/fnv"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/lni/goutils/syncutil"
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
		processForwardUserActionC: make(chan UserAction, 1024),
		processNodePingC:          make(chan *nodePingReq, 1024),
		processNodePongC:          make(chan *nodePongReq, 1024),
		processProxyNodeTimeoutC:  make(chan *proxyNodeTimeoutReq, 1024),
		processCloseC:             make(chan *userCloseReq, 1024),
		stopper:                   syncutil.NewStopper(),
		Log:                       wklog.NewWKLog(fmt.Sprintf("userReactor[%d]", s.opts.Cluster.NodeId)),
		s:                         s,
	}

	u.subs = make([]*userReactorSub, s.opts.Reactor.UserSubCount)
	for i := 0; i < s.opts.Reactor.UserSubCount; i++ {
		sub := newUserReactorSub(i, u)
		u.subs[i] = sub
	}

	return u
}

func (u *userReactor) start() error {

	for i := 0; i < 100; i++ {
		u.stopper.RunWorker(u.processInitLoop)
		u.stopper.RunWorker(u.processAuthLoop)
		u.stopper.RunWorker(u.processPingLoop)
		u.stopper.RunWorker(u.processWriteLoop)
		u.stopper.RunWorker(u.processRecvackLoop)
		u.stopper.RunWorker(u.processForwardUserActionLoop)
		u.stopper.RunWorker(u.processNodePingLoop)
		u.stopper.RunWorker(u.processNodePongLoop)
		u.stopper.RunWorker(u.processProxyNodeTimeoutLoop)
		u.stopper.RunWorker(u.processCloseLoop)
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

	fmt.Println("userReactor stop--->1")
	for _, sub := range u.subs {
		sub.stop()
	}
	fmt.Println("userReactor stop--->2")
}

func (u *userReactor) addUserIfNotExist(h *userHandler) {
	u.reactorSub(h.uid).addUserIfNotExist(h)
}

func (u *userReactor) getUser(uid string) *userHandler {
	return u.reactorSub(uid).getUser(uid)
}

func (u *userReactor) existUser(uid string) bool {
	return u.reactorSub(uid).existUser(uid)
}

func (u *userReactor) removeUserByUniqueNo(uid string, uniqueNo string) {
	u.reactorSub(uid).removeUserByUniqueNo(uid, uniqueNo)
}

func (u *userReactor) removeUser(uid string) {
	u.reactorSub(uid).removeUser(uid)
}

func (u *userReactor) addConnContext(conn *connContext) {
	u.reactorSub(conn.uid).addConnContext(conn)
}

func (u *userReactor) getConnContext(uid string, deviceId string) *connContext {
	return u.reactorSub(uid).getConnContext(uid, deviceId)
}

func (u *userReactor) getConnContextById(uid string, connId int64) *connContext {
	return u.reactorSub(uid).getConnContextById(uid, connId)
}

func (u *userReactor) getConnContextByProxyConnId(uid string, nodeId uint64, proxyConnId int64) *connContext {
	return u.reactorSub(uid).getConnContextByProxyConnId(uid, nodeId, proxyConnId)
}
func (u *userReactor) getConnContexts(uid string) []*connContext {
	return u.reactorSub(uid).getConnContexts(uid)
}

func (u *userReactor) getConnContextByDeviceFlag(uid string, deviceFlag wkproto.DeviceFlag) []*connContext {
	return u.reactorSub(uid).getConnContextByDeviceFlag(uid, deviceFlag)
}

func (u *userReactor) getConnContextCountByDeviceFlag(uid string, deviceFlag wkproto.DeviceFlag) int {
	return len(u.getConnContextByDeviceFlag(uid, deviceFlag))
}

func (u *userReactor) getConnContextCount(uid string) int {
	return u.reactorSub(uid).getConnContextCount(uid)
}

// func (u *userReactor) removeConnContext(uid string, deviceId string) {
// 	u.reactorSub(uid).removeConnContext(uid, deviceId)
// }

func (u *userReactor) removeConnContextById(uid string, id int64) *connContext {
	return u.reactorSub(uid).removeConnContextById(uid, id)
}

// 移除指定节点的所有连接
func (u *userReactor) removeConnsByNodeId(uid string, nodeId uint64) []*connContext {
	return u.reactorSub(uid).removeConnsByNodeId(uid, nodeId)
}

func (u *userReactor) reactorSub(uid string) *userReactorSub {

	h := fnv.New32a()
	h.Write([]byte(uid))

	i := h.Sum32() % uint32(len(u.subs))
	return u.subs[i]
}

func (u *userReactor) step(uid string, a UserAction) {
	u.reactorSub(uid).step(uid, a)
}

func (u *userReactor) writePacket(conn *connContext, packet wkproto.Frame) error {
	return u.reactorSub(conn.uid).writePacket(conn, packet)
}

func (u *userReactor) writePacketByDeviceId(uid string, deviceId string, packet wkproto.Frame) error {
	conn := u.getConnContext(uid, deviceId)
	if conn == nil {
		u.Error("conn not found", zap.String("uid", uid), zap.String("deviceId", deviceId))
		return ErrConnNotFound
	}
	return u.reactorSub(uid).writePacket(conn, packet)
}

func (u *userReactor) writePacketByConnId(uid string, connId int64, packet wkproto.Frame) error {
	conn := u.getConnContextById(uid, connId)
	if conn == nil {
		u.Error("conn not found", zap.String("uid", uid), zap.Int64("connId", connId), zap.String("frameType", packet.GetFrameType().String()))
		return ErrConnNotFound
	}
	return u.reactorSub(uid).writePacket(conn, packet)
}
