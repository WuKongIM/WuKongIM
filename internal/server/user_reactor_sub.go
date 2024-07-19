package server

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/lni/goutils/syncutil"
	"go.uber.org/zap"
)

type userReactorSub struct {
	stopper  *syncutil.Stopper
	r        *userReactor
	users    *userList
	advanceC chan struct{}
	wklog.Log
	index int

	stepUserC chan stepUser

	mu sync.Mutex
}

func newUserReactorSub(index int, r *userReactor) *userReactorSub {
	return &userReactorSub{
		stopper:   syncutil.NewStopper(),
		users:     newUserList(),
		r:         r,
		Log:       wklog.NewWKLog(fmt.Sprintf("userReactorSub[%d][%d]", r.s.opts.Cluster.NodeId, index)),
		index:     index,
		advanceC:  make(chan struct{}, 1),
		stepUserC: make(chan stepUser, 1024),
	}
}

func (u *userReactorSub) start() error {
	u.stopper.RunWorker(u.loop)
	return nil
}

func (u *userReactorSub) stop() {
	u.stopper.Stop()
}

func (u *userReactorSub) loop() {
	tk := time.NewTicker(time.Millisecond * 200)
	for {
		u.readys()
		select {
		case <-tk.C:
			u.ticks()
		case <-u.advanceC:
		case req := <-u.stepUserC:
			userHanlder := u.getUser(req.uid)
			if userHanlder != nil {
				if req.action.UniqueNo == "" {
					req.action.UniqueNo = userHanlder.uniqueNo
				}
				err := userHanlder.step(req.action)
				if req.waitC != nil {
					req.waitC <- err
				}
			} else {
				u.Warn("loop: user not found", zap.String("uid", req.uid), zap.String("action", req.action.ActionType.String()))
				if req.waitC != nil {
					req.waitC <- errors.New("user not found")
				}
			}
		case <-u.stopper.ShouldStop():
			return
		}
	}
}

func (u *userReactorSub) step(uid string, action UserAction) {

	select {
	case u.stepUserC <- stepUser{
		uid:    uid,
		action: action,
		waitC:  nil,
	}:
	case <-u.stopper.ShouldStop():
		return
	}
}

func (u *userReactorSub) stepNoWait(uid string, action UserAction) error {
	select {
	case u.stepUserC <- stepUser{
		uid:    uid,
		action: action,
		waitC:  nil,
	}:
	default:
		return errors.New("stepUserC full")
	}
	return nil
}

func (u *userReactorSub) proposeSend(conn *connContext, sendPacket *wkproto.SendPacket) error {

	return u.r.s.channelReactor.proposeSend(conn.uid, conn.deviceId, conn.connId, u.r.s.opts.Cluster.NodeId, true, sendPacket)
}

func (u *userReactorSub) stepWait(uid string, action UserAction) error {
	waitC := make(chan error, 1)
	select {
	case u.stepUserC <- stepUser{
		uid:    uid,
		action: action,
		waitC:  waitC,
	}:
	case <-u.stopper.ShouldStop():
		return ErrReactorStopped
	}

	select {
	case err := <-waitC:
		return err
	case <-u.stopper.ShouldStop():
		return ErrReactorStopped
	}
}

func (u *userReactorSub) readys() {
	u.users.iter(func(uh *userHandler) bool {
		if uh.hasReady() {
			u.handleReady(uh)
		}
		return true
	})
}

func (u *userReactorSub) ticks() {
	u.users.iter(func(uh *userHandler) bool {
		uh.tick()
		return true
	})
}

func (u *userReactorSub) handleReady(uh *userHandler) {
	rd := uh.ready()
	for _, action := range rd.actions {
		switch action.ActionType {
		case UserActionInit: // 用户初始化请求
			u.r.addInitReq(&userInitReq{
				uniqueNo: action.UniqueNo,
				uid:      uh.uid,
			})
		case UserActionAuth: // 用户连接认证请求
			u.r.addAuthReq(&userAuthReq{
				uniqueNo: action.UniqueNo,
				uid:      uh.uid,
				messages: action.Messages,
			})
		case UserActionPing: // 用户发送ping
			u.r.addPingReq(&pingReq{
				uniqueNo: action.UniqueNo,
				uid:      uh.uid,
				messages: action.Messages,
			})
		case UserActionRecvack: // 用户发送recvack
			u.r.addRecvackReq(&recvackReq{
				uniqueNo: action.UniqueNo,
				uid:      uh.uid,
				messages: action.Messages,
			})
		case UserActionRecv: // 用户接受消息
			u.r.addWriteReq(&writeReq{
				uniqueNo: action.UniqueNo,
				uid:      uh.uid,
				messages: action.Messages,
			})
		case UserActionForward: // 转发action
			u.r.addForwardUserActionReq(action)
		case UserActionNodePing: // 用户节点ping, 用户的领导发送给追随者的ping
			u.r.addNodePingReq(&nodePingReq{
				uid:      uh.uid,
				messages: action.Messages,
			})
		case UserActionNodePong: // 用户节点pong, 用户的追随者返回给领导的pong
			u.r.addNodePongReq(&nodePongReq{
				uniqueNo: action.UniqueNo,
				uid:      uh.uid,
				leaderId: action.LeaderId,
			})

		case UserActionProxyNodeTimeout: // 用户节点pong超时
			u.r.addProxyNodeTimeoutReq(&proxyNodeTimeoutReq{
				uniqueNo: action.UniqueNo,
				uid:      uh.uid,
				messages: action.Messages,
			})
		case UserActionClose: // 用户关闭
			u.r.addCloseReq(&userCloseReq{
				uniqueNo: action.UniqueNo,
				uid:      uh.uid,
			})
		case UserActionCheckLeader: // 检查领导的正确性
			leaderId := uh.leaderId
			if uh.role == userRoleLeader {
				leaderId = u.r.s.opts.Cluster.NodeId
			}
			u.r.addCheckLeaderReq(&checkLeaderReq{
				uid:      uh.uid,
				uniqueNo: action.UniqueNo,
				leaderId: leaderId,
			})

		default:
			u.Error("unknown action type", zap.String("actionType", action.ActionType.String()))
		}
		// msgs := a.Messages
		// for _, msg := range msgs {
		// 	if msg.InPacket != nil {
		// 		u.handleOtherPackets(msg.Uid, msg.DeviceId, msg.ConnId, msg.InPacket)
		// 	}
		// 	if len(msg.OutBytes) > 0 {
		// 		u.handleOutBytes(msg.Uid, msg.DeviceId, msg.OutBytes)
		// 	}
		// }
	}
}

func (u *userReactorSub) advance() {
	select {
	case u.advanceC <- struct{}{}:
	default:
	}

}

func (u *userReactorSub) addUserIfNotExist(h *userHandler) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.getUser(h.uid) == nil {
		u.Info("add user", zap.String("uid", h.uid))
		u.users.add(h)
	}
}

func (u *userReactorSub) getUser(uid string) *userHandler {
	return u.users.get(uid)
}

func (u *userReactorSub) existUser(uid string) bool {
	return u.users.exist(uid)
}

func (u *userReactorSub) removeUser(uid string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.users.remove(uid)
}

func (u *userReactorSub) removeUserByUniqueNo(uid string, uniqueNo string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	user := u.users.get(uid)
	if user != nil {
		if user.uniqueNo == uniqueNo {
			u.users.remove(uid)
		}
	}
}

func (u *userReactorSub) getConnsByUniqueNo(uid string, uniqueNo string) []*connContext {
	u.mu.Lock()
	defer u.mu.Unlock()
	user := u.users.get(uid)
	if user != nil {
		if user.uniqueNo == uniqueNo {
			newConns := make([]*connContext, len(user.conns))
			copy(newConns, user.conns)
			return newConns
		}
	}
	return nil
}

func (u *userReactorSub) getConnContext(uid string, deviceId string) *connContext {
	uh := u.getUser(uid)
	if uh == nil {
		return nil
	}
	return uh.getConn(deviceId)
}

func (u *userReactorSub) getConnContextById(uid string, id int64) *connContext {
	uh := u.getUser(uid)
	if uh == nil {
		return nil
	}
	return uh.getConnById(id)
}

func (u *userReactorSub) getConnContextByProxyConnId(uid string, nodeId uint64, proxyConnId int64) *connContext {
	uh := u.getUser(uid)
	if uh == nil {
		return nil
	}
	return uh.getConnByProxyConnId(nodeId, proxyConnId)
}

func (u *userReactorSub) getConnContexts(uid string) []*connContext {
	uh := u.getUser(uid)
	if uh == nil {
		return nil
	}
	return uh.getConns()
}

func (u *userReactorSub) getConnContextCountByDeviceFlag(uid string, deviceFlag wkproto.DeviceFlag) int {
	return len(u.getConnContextByDeviceFlag(uid, deviceFlag))
}

func (u *userReactorSub) getConnContextCount(uid string) int {
	uh := u.getUser(uid)
	if uh == nil {
		return 0
	}
	return uh.getConnCount()
}

func (u *userReactorSub) getConnContextByDeviceFlag(uid string, deviceFlag wkproto.DeviceFlag) []*connContext {
	var conns []*connContext
	u.users.iter(func(uh *userHandler) bool {
		for _, c := range uh.conns {
			if c.uid == uid && c.deviceFlag == deviceFlag {
				conns = append(conns, c)
			}
		}
		return true
	})
	return conns
}

// func (u *userReactorSub) removeConnContext(uid string, deviceId string) {
// 	u.mu.Lock()
// 	defer u.mu.Unlock()

// 	uh := u.getUser(uid)
// 	if uh == nil {
// 		return
// 	}
// 	uh.removeConn(deviceId)

// 	if uh.getConnCount() <= 0 {
// 		u.users.remove(uh.uid)
// 	}
// }

func (u *userReactorSub) removeConnContextById(uid string, id int64) *connContext {
	u.mu.Lock()
	defer u.mu.Unlock()

	uh := u.getUser(uid)
	if uh == nil {
		return nil
	}
	conn := uh.removeConnById(id)

	if uh.getConnCount() <= 0 {
		u.Info("remove user", zap.String("uid", uh.uid))
		u.users.remove(uh.uid)
	}
	return conn
}

func (u *userReactorSub) removeConnsByNodeId(uid string, nodeId uint64) []*connContext {
	u.mu.Lock()
	defer u.mu.Unlock()
	uh := u.getUser(uid)
	if uh == nil {
		return nil
	}
	conns := uh.removeConnsByNodeId(nodeId)
	if uh.getConnCount() <= 0 {
		u.users.remove(uh.uid)
		u.Info("remove user", zap.String("uid", uh.uid))
	}
	return conns
}

func (u *userReactorSub) addConnContext(conn *connContext) {

	uh := u.getUser(conn.uid)
	if uh == nil {
		uh = newUserHandler(conn.uid, u)
		u.addUserIfNotExist(uh)
	}
	uh.addConnIfNotExist(conn)
}

// func (u *userReactorSub) removeConn(uid string, deviceId string) {
// 	u.mu.Lock()
// 	defer u.mu.Unlock()

// 	uh := u.getUser(uid)
// 	if uh == nil {
// 		return
// 	}
// 	uh.removeConn(deviceId)
// 	if uh.getConnCount() <= 0 {
// 		u.users.remove(uh.uid)
// 	}
// }

func (u *userReactorSub) writePacket(conn *connContext, packet wkproto.Frame) error {

	return conn.writePacket(packet)
}

type stepUser struct {
	uid    string
	action UserAction
	waitC  chan error
}
