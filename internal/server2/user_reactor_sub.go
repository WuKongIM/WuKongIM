package server

import (
	"context"
	"fmt"
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

func (u *userReactorSub) step(uid string, action *UserAction) error {

	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	select {
	case u.stepUserC <- stepUser{
		uid:    uid,
		action: action,
		waitC:  nil,
	}:
	case <-timeoutCtx.Done():
		u.Error("step timeout", zap.String("uid", uid))
		return timeoutCtx.Err()
	case <-u.stopper.ShouldStop():
		return ErrReactorStopped
	}
	return nil
}

func (u *userReactorSub) proposeSend(conn *connContext, sendPacket *wkproto.SendPacket) error {

	return u.r.s.channelReactor.proposeSend(conn.uid, conn.deviceId, sendPacket)
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
			rd := uh.ready()
			for _, a := range rd.actions {

				msgs := a.Messages
				for _, msg := range msgs {
					if msg.InPacket != nil {
						if msg.InPacket.GetFrameType() == wkproto.SEND {
							u.handleSendPackets(msg.Uid, msg.DeviceId, msg.InPacket.(*wkproto.SendPacket))
						} else {
							u.handleOtherPackets(msg.Uid, msg.DeviceId, msg.InPacket)
						}

					}
					if len(msg.OutBytes) > 0 {
						u.handleOutBytes(msg.Uid, msg.DeviceId, msg.OutBytes)
					}
				}

			}
		}
		return true
	})
}

func (u *userReactorSub) advance() {
	select {
	case u.advanceC <- struct{}{}:
	default:
	}

}

func (u *userReactorSub) handleOtherPackets(fromUid string, fromDeviceId string, packet wkproto.Frame) {
	switch packet.GetFrameType() {
	case wkproto.PING:
		u.r.addPingReq(&pingReq{
			fromUid:      fromUid,
			fromDeviceId: fromDeviceId,
		})
	case wkproto.RECVACK:
		u.r.addRecvackReq(&recvackReq{
			fromUid:      fromUid,
			fromDeviceId: fromDeviceId,
			recvack:      packet.(*wkproto.RecvackPacket),
		})
	default:
		u.Error("unknown frame type", zap.String("frameType", packet.GetFrameType().String()))
	}
}

func (u *userReactorSub) handleSendPackets(fromUid string, fromDeviceId string, sendPacket *wkproto.SendPacket) {
	err := u.r.s.channelReactor.proposeSend(fromUid, fromDeviceId, sendPacket)
	if err != nil {
		u.r.Error("propose send error", zap.Error(err))
	}
}

func (u *userReactorSub) handleOutBytes(fromUid string, fromDeviceId string, outBytes []byte) {
	if len(outBytes) == 0 {
		return
	}
	u.r.addWriteReq(&writeReq{
		toUid:      fromUid,
		toDeviceId: fromDeviceId,
		data:       outBytes,
	})

}

func (u *userReactorSub) addUserIfNotExist(h *userHandler) {
	if u.getUser(h.uid) == nil {
		u.users.add(h)
	}
}

func (u *userReactorSub) getUser(uid string) *userHandler {
	return u.users.get(uid)
}

func (u *userReactorSub) getConnContext(uid string, deviceId string) *connContext {
	uh := u.getUser(uid)
	if uh == nil {
		return nil
	}
	return uh.getConn(deviceId)
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
	uh := u.getUser(uid)
	if uh == nil {
		return
	}
	uh.removeConn(deviceId)
}

func (u *userReactorSub) addConnContext(conn *connContext) {
	fmt.Println("conn.uid--->", conn.uid)
	uh := u.getUser(conn.uid)
	if uh == nil {
		uh = newUserHandler(conn.uid)
		u.addUserIfNotExist(uh)
	}
	uh.addConnIfNotExist(conn)
}

func (u *userReactorSub) addConnIfNotExist(conn *connContext) {
	u.users.iter(func(uh *userHandler) bool {
		if uh.uid == conn.uid {
			uh.addConnIfNotExist(conn)
			return false
		}
		return true
	})
}

func (u *userReactorSub) removeConn(uid string, deviceId string) {
	u.users.iter(func(uh *userHandler) bool {
		if uh.uid == uid {
			uh.removeConn(deviceId)
			return false
		}
		return true
	})
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
