package server

import (
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

type connInfo struct {
	connId       int64 // 连接在本节点的id
	proxyConnId  int64 // 连接在代理节点的id
	uid          string
	deviceId     string
	deviceFlag   wkproto.DeviceFlag
	deviceLevel  wkproto.DeviceLevel
	aesKey       string
	aesIV        string
	protoVersion uint8

	closed atomic.Bool

	isAuth atomic.Bool // 是否已经认证
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
				ConnId:     c.connId,
				DeviceId:   c.deviceId,
				InPacket:   packet,
				FromNodeId: c.subReactor.r.s.opts.Cluster.NodeId,
			},
		},
	})
}

func (c *connContext) addConnectPacket(packet *wkproto.ConnectPacket) {
	err := c.subReactor.stepNoWait(c.uid, &UserAction{
		ActionType: UserActionConnect,
		Messages: []*ReactorUserMessage{
			{
				ConnId:     c.connId,
				DeviceId:   c.deviceId,
				InPacket:   packet,
				FromNodeId: c.subReactor.r.s.opts.Cluster.NodeId,
			},
		},
	})
	if err != nil {
		wklog.Error("addConnectPacket error", zap.String("uid", c.uid), zap.Error(err))
	}
}

func (c *connContext) addSendPacket(packet *wkproto.SendPacket) {

	_ = c.subReactor.proposeSend(c, packet)
}

func (c *connContext) write(d []byte) error {
	return c.subReactor.stepNoWait(c.uid, &UserAction{
		ActionType: UserActionRecv,
		Messages: []*ReactorUserMessage{
			{
				ConnId:     c.connId,
				DeviceId:   c.deviceId,
				OutBytes:   d,
				FromNodeId: c.subReactor.r.s.opts.Cluster.NodeId,
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
