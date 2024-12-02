package server

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/trace"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/atomic"
	"go.uber.org/zap"
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
	connInfo   // 连接信息
	connStats  // 统计信息
	conn       wknet.Conn
	subReactor *userReactorSub

	realNodeId uint64 // 真实的节点id
	isRealConn bool   // 是否是真实的连接

	uptime atomic.Time // 启动时间

	lastActivity atomic.Int64 // 最后活动时间

	wklog.Log
}

func newConnContext(connInfo connInfo, conn wknet.Conn, subReactor *userReactorSub) *connContext {
	c := &connContext{
		connInfo:   connInfo,
		conn:       conn,
		subReactor: subReactor,
		isRealConn: true,
		Log:        wklog.NewWKLog(fmt.Sprintf("connContext[%s]", connInfo.uid)),
	}

	c.uptime.Store(time.Now())

	return c
}

func newConnContextProxy(realNodeId uint64, connInfo connInfo, subReactor *userReactorSub) *connContext {
	return &connContext{
		connInfo:   connInfo,
		subReactor: subReactor,
		realNodeId: realNodeId,
		isRealConn: false,
		Log:        wklog.NewWKLog(fmt.Sprintf("connContext[%s]", connInfo.uid)),
	}
}

func (c *connContext) addOtherPacket(packet wkproto.Frame) {

	// 保持活动
	c.keepActivity()

	// 数据统计
	frameSize := packet.GetFrameSize()
	c.inPacketCount.Add(1)
	c.inPacketByteCount.Add(frameSize)

	if packet.GetFrameType() == wkproto.PING {
		trace.GlobalTrace.Metrics.App().PingCountAdd(1)
		trace.GlobalTrace.Metrics.App().PingBytesAdd(frameSize)
	}

	// 预先分配一个长度为1的切片，避免在创建UserAction时动态分配内存
	messages := make([]ReactorUserMessage, 1)
	messages[0] = ReactorUserMessage{
		ConnId:     c.connId,
		DeviceId:   c.deviceId,
		InPacket:   packet,
		FromNodeId: c.subReactor.r.s.opts.Cluster.NodeId,
	}

	c.subReactor.step(c.uid, UserAction{
		ActionType: UserActionSend,
		Messages:   messages,
	})
}

func (c *connContext) addConnectPacket(packet *wkproto.ConnectPacket) {
	// 保持活动
	c.keepActivity()

	// 数据统计
	c.inPacketCount.Add(1)

	frameSize := packet.GetFrameSize()
	c.inPacketByteCount.Add(frameSize)

	trace.GlobalTrace.Metrics.App().ConnPacketCountAdd(1)
	trace.GlobalTrace.Metrics.App().ConnPacketBytesAdd(frameSize)

	// 预先分配一个长度为1的切片，避免在创建UserAction时动态分配内存
	messages := make([]ReactorUserMessage, 1)
	messages[0] = ReactorUserMessage{
		ConnId:     c.connId,
		DeviceId:   c.deviceId,
		InPacket:   packet,
		FromNodeId: c.subReactor.r.s.opts.Cluster.NodeId,
	}
	err := c.subReactor.stepNoWait(c.uid, UserAction{
		ActionType: UserActionConnect,
		Messages:   messages,
	})
	if err != nil {
		wklog.Error("addConnectPacket error", zap.String("uid", c.uid), zap.Error(err))
	}
}

func (c *connContext) addSendPacket(packet *wkproto.SendPacket) {

	// 保持活动
	c.keepActivity()

	// 数据统计
	frameSize := packet.GetFrameSize()
	c.inPacketCount.Add(1)
	c.inPacketByteCount.Add(frameSize)

	c.inMsgCount.Add(1)
	c.inMsgByteCount.Add(frameSize)

	trace.GlobalTrace.Metrics.App().SendPacketCountAdd(1)
	trace.GlobalTrace.Metrics.App().SendPacketBytesAdd(frameSize)

	messageId := c.subReactor.r.s.channelReactor.messageIDGen.Generate().Int64()

	c.MessageTrace("收到消息", packet.ClientMsgNo, "processMessage", zap.Int64("messageId", messageId), zap.String("channelId", packet.ChannelID), zap.Uint8("channelType", packet.ChannelType), zap.String("uid", c.uid), zap.String("deviceId", c.deviceId), zap.Uint8("deviceFlag", uint8(c.deviceFlag)), zap.Uint8("deviceLevel", uint8(c.deviceLevel)))

	// 非法频道id，直接返回发送失败
	if strings.TrimSpace(packet.ChannelID) == "" || IsSpecialChar(packet.ChannelID) {
		c.Error("addSendPacket failed, channelId is illegal", zap.String("uid", c.uid), zap.String("channelId", packet.ChannelID))
		c.MessageTrace("addSendPacket failed, channelId is illegal", packet.ClientMsgNo, "processMessage", zap.Error(errors.New("channelId is illegal")))
		sendack := &wkproto.SendackPacket{
			Framer:      packet.Framer,
			MessageID:   messageId,
			ClientSeq:   packet.ClientSeq,
			ClientMsgNo: packet.ClientMsgNo,
			ReasonCode:  wkproto.ReasonChannelIDError,
		}
		_ = c.writeDirectlyPacket(sendack)
		return
	}

	// 提案发送至频道
	_ = c.subReactor.proposeSend(c, messageId, packet, false)

}

func (c *connContext) writePacket(packet wkproto.Frame) error {
	data, err := c.subReactor.r.s.opts.Proto.EncodeFrame(packet, c.protoVersion)
	if err != nil {
		return err
	}
	return c.write(data, packet.GetFrameType())
}

func (c *connContext) write(d []byte, frameType wkproto.FrameType) error {

	msgs := make([]ReactorUserMessage, 1)
	msgs[0] = ReactorUserMessage{
		ConnId:     c.connId,
		DeviceId:   c.deviceId,
		FrameType:  frameType,
		OutBytes:   d,
		FromNodeId: c.subReactor.r.s.opts.Cluster.NodeId,
	}

	c.subReactor.step(c.uid, UserAction{
		ActionType: UserActionRecv,
		Messages:   msgs,
	})
	return nil
}

func (c *connContext) writeDirectlyPacket(packet wkproto.Frame) error {

	data, err := c.subReactor.r.s.opts.Proto.EncodeFrame(packet, c.protoVersion)
	if err != nil {
		return err
	}

	var count = uint32(0)
	if packet.GetFrameType() == wkproto.RECV {
		count = 1
	}

	return c.writeDirectly(data, count)
}

// 直接写入连接
func (c *connContext) writeDirectly(data []byte, recvFrameCount uint32) error {

	dataSize := int64(len(data))
	if recvFrameCount > 0 {
		c.outMsgCount.Add(int64(recvFrameCount))
		c.outMsgByteCount.Add(dataSize) // TODO: 这里其实有点不准确，因为data不一定都是recv包, 但是大体上recv包占大多数

	}

	c.outPacketCount.Add(1)
	c.outPacketByteCount.Add(dataSize)

	if c.conn == nil {
		c.Error("writeDirectly failed, conn is nil", zap.String("conn", c.String()))
		return errors.New("writeDirectly failed, conn is nil")
	}
	conn := c.conn
	wsConn, wsok := conn.(wknet.IWSConn) // websocket连接
	if wsok {
		err := wsConn.WriteServerBinary(data)
		if err != nil {
			c.Warn("Failed to write the message", zap.Error(err))
		}

	} else {
		_, err := conn.WriteToOutboundBuffer(data)
		if err != nil {
			c.Warn("Failed to write the message", zap.Error(err))
		}
	}
	return conn.WakeWrite()
}

func (c *connContext) keepActivity() {
	c.lastActivity.Store(time.Now().Unix())
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
	if !c.isRealConn {
		c.subReactor.r.s.onCloseForProxy(c)
	}

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
