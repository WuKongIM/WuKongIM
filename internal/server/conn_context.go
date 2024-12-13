package server

import (
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/atomic"
)

type connStats struct {
	inPacketCount  atomic.Int64 // 输入包数量
	outPacketCount atomic.Int64 // 输出包数量

	inPacketByteCount  atomic.Int64 // 输入包字节数量
	outPacketByteCount atomic.Int64 // 输出包字节数量

	inMsgCount  atomic.Int64 // 输入消息数量
	outMsgCount atomic.Int64 // 输出消息数量

	inMsgByteCount  atomic.Int64 // 输入消息字节数量
	outMsgByteCount atomic.Int64 // 输出消息字节数量
}

type connInfo struct {
	connId       int64 // 连接在本节点的id
	proxyConnId  int64 // 连接在代理节点的id
	uid          string
	deviceId     string
	deviceFlag   wkproto.DeviceFlag
	deviceLevel  wkproto.DeviceLevel
	aesKey       []byte
	aesIV        []byte
	protoVersion uint8

	closed atomic.Bool

	isAuth atomic.Bool // 是否已经认证

}

type connContext struct {
	connInfo  // 连接信息
	connStats // 统计信息
	conn      wknet.Conn

	realNodeId uint64 // 真实的节点id
	isRealConn bool   // 是否是真实的连接

	uptime atomic.Time // 启动时间

	lastActivity atomic.Int64 // 最后活动时间

	wklog.Log
}

func newConnContext(connInfo connInfo, conn wknet.Conn) *connContext {
	c := &connContext{
		connInfo:   connInfo,
		conn:       conn,
		isRealConn: true,
		Log:        wklog.NewWKLog(fmt.Sprintf("connContext[%s]", connInfo.uid)),
	}

	c.uptime.Store(time.Now())

	return c
}

func newConnContextProxy(realNodeId uint64, connInfo connInfo) *connContext {
	return &connContext{
		connInfo:   connInfo,
		realNodeId: realNodeId,
		isRealConn: false,
		Log:        wklog.NewWKLog(fmt.Sprintf("connContext[%s]", connInfo.uid)),
	}
}

func (c *connContext) keepActivity() {
	c.lastActivity.Store(time.Now().Unix())
}

func (c *connContext) isClosed() bool {
	return c.closed.Load()
}

func (c *connContext) String() string {
	fd := 0
	if c.conn != nil {
		fd = c.conn.Fd().Fd()
	}
	return fmt.Sprintf("uid: %s connId: %d deviceId: %s deviceFlag: %s isRealConn: %v proxyConnId: %d realNodeId: %d fd: %d", c.uid, c.connId, c.deviceId, c.deviceFlag.String(), c.isRealConn, c.proxyConnId, c.realNodeId, fd)
}

func (c *connContext) ConnId() int64 {
	return c.connId
}
func (c *connContext) Uid() string {
	return c.uid
}
func (c *connContext) FromNode() uint64 {
	return c.realNodeId
}
func (c *connContext) SetAuth(auth bool) {
	c.isAuth.Store(auth)
}
func (c *connContext) IsAuth() bool {
	return c.isAuth.Load()
}

func (c *connContext) DeviceFlag() wkproto.DeviceFlag {
	return c.deviceFlag
}
