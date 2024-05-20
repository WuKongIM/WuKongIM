package server

import (
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

type connInfo struct {
	id           int64
	uid          string
	deviceId     string
	deviceFlag   wkproto.DeviceFlag
	deviceLevel  wkproto.DeviceLevel
	aesKey       string
	aesIV        string
	protoVersion uint8

	closed atomic.Bool
}

type connContext struct {
	connInfo
	conn       wknet.Conn
	subReactor *userReactorSub

	realNodeId uint64 // 真实的节点id
	isRealConn bool   // 是否是真实的连接
}

func newConnContext(connInfo connInfo, conn wknet.Conn, subReactor *userReactorSub) *connContext {
	return &connContext{
		connInfo:   connInfo,
		conn:       conn,
		subReactor: subReactor,
		isRealConn: true,
	}
}

func newConnContextProxy(realNodeId uint64, connInfo connInfo, subReactor *userReactorSub) *connContext {
	return &connContext{
		connInfo:   connInfo,
		subReactor: subReactor,
		realNodeId: realNodeId,
		isRealConn: false,
	}
}

func (c *connContext) addOtherPacket(packet wkproto.Frame) {
	c.subReactor.step(c.uid, &UserAction{
		ActionType: UserActionSend,
		Messages: []*ReactorUserMessage{
			{
				ConnId:   c.id,
				Uid:      c.uid,
				DeviceId: c.deviceId,
				InPacket: packet,
			},
		},
	})
}

func (c *connContext) addSendPacket(packet *wkproto.SendPacket) {

	_ = c.subReactor.proposeSend(c, packet)
}

func (c *connContext) write(d []byte) {
	c.subReactor.step(c.uid, &UserAction{
		ActionType: UserActionRecv,
		Messages: []*ReactorUserMessage{
			{
				ConnId:   c.id,
				Uid:      c.uid,
				DeviceId: c.deviceId,
				OutBytes: d,
			},
		},
	})
}

func (c *connContext) close() {
	if c.closed.Load() {
		return
	}
	c.closed.Store(true)
	if c.conn != nil {
		err := c.conn.Close()
		if err != nil {
			wklog.Error("conn close error", zap.Error(err))
		}
	}
}

func (c *connContext) isClosed() bool {
	return c.closed.Load()
}
