package server

import (
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
		Log:       wklog.NewWKLog(fmt.Sprintf("userReactorSub[%d]", index)),
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
		case <-u.advanceC:
		case req := <-u.stepUserC:
			userHanlder := u.getUser(req.uid)
			if userHanlder != nil {
				err := userHanlder.step(req.action)
				if req.waitC != nil {
					req.waitC <- err
				}
			}
		case <-u.stopper.ShouldStop():
			return
		}
	}
}

func (u *userReactorSub) step(uid string, action *UserAction) {

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

func (u *userReactorSub) proposeSend(conn *connContext, sendPacket *wkproto.SendPacket) error {

	return u.r.s.channelReactor.proposeSend(conn.uid, conn.deviceId, conn.id, u.r.s.opts.Cluster.NodeId, true, sendPacket)
}

func (u *userReactorSub) stepWait(uid string, action *UserAction) error {
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

func (u *userReactorSub) handleReady(uh *userHandler) {
	rd := uh.ready()
	for _, action := range rd.actions {
		switch action.ActionType {
		case UserActionInit: // 用户初始化请求
			u.r.addInitReq(&userInitReq{
				uid: uh.uid,
			})
		case UserActionPing: // 用户发送ping
			u.r.addPingReq(&pingReq{
				uid:      uh.uid,
				messages: action.Messages,
			})
		case UserActionRecvack: // 用户发送recvack
			u.r.addRecvackReq(&recvackReq{
				uid:      uh.uid,
				messages: action.Messages,
			})
		case UserActionRecv: // 用户接受消息
			u.r.addWriteReq(&writeReq{
				uid:      uh.uid,
				messages: action.Messages,
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
		u.users.add(h)
	}
}

func (u *userReactorSub) getUser(uid string) *userHandler {
	return u.users.get(uid)
}

func (u *userReactorSub) existUser(uid string) bool {
	return u.users.exist(uid)
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

func (u *userReactorSub) removeConnContext(uid string, deviceId string) {
	u.mu.Lock()
	defer u.mu.Unlock()

	uh := u.getUser(uid)
	if uh == nil {
		return
	}
	uh.removeConn(deviceId)

	if uh.getConnCount() <= 0 {
		u.users.remove(uh.uid)
	}
}

func (u *userReactorSub) removeConnContextById(uid string, id int64) {
	u.mu.Lock()
	defer u.mu.Unlock()

	uh := u.getUser(uid)
	if uh == nil {
		return
	}
	uh.removeConnById(id)

	if uh.getConnCount() <= 0 {
		u.users.remove(uh.uid)
	}
}

func (u *userReactorSub) addConnContext(conn *connContext) {

	uh := u.getUser(conn.uid)
	if uh == nil {
		uh = newUserHandler(conn.uid, u)
		u.addUserIfNotExist(uh)
	}
	uh.addConnIfNotExist(conn)
}

func (u *userReactorSub) removeConn(uid string, deviceId string) {
	u.mu.Lock()
	defer u.mu.Unlock()

	uh := u.getUser(uid)
	if uh == nil {
		return
	}
	uh.removeConn(deviceId)
	if uh.getConnCount() <= 0 {
		u.users.remove(uh.uid)
	}
}

func (u *userReactorSub) writePacket(conn *connContext, packet wkproto.Frame) error {
	data, err := u.r.s.opts.Proto.EncodeFrame(packet, conn.protoVersion)
	if err != nil {
		return err
	}
	conn.write(data)
	return nil
}

type stepUser struct {
	uid    string
	action *UserAction
	waitC  chan error
}
