package server

import (
	"strings"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/trace"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

type Dispatch struct {
	engine *wknet.Engine

	s         *Server
	processor *Processor
	wklog.Log
	framePool sync.Pool
}

func NewDispatch(s *Server) *Dispatch {
	return &Dispatch{
		engine: wknet.NewEngine(
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
		),
		s:         s,
		processor: NewProcessor(s),
		Log:       wklog.NewWKLog("Dispatch"),
		framePool: sync.Pool{
			New: func() any {
				return make([]wkproto.Frame, 20)
			},
		},
	}
}

// conn是否允许
func (d *Dispatch) connIsAllow(conn wknet.Conn) bool {
	connRemoteAddr := conn.RemoteAddr()
	if connRemoteAddr != nil {
		// 获取IP地址
		ip := strings.Split(connRemoteAddr.String(), ":")[0]
		// 判断是否在黑名单中
		if !d.s.AllowIP(ip) {
			d.Debug("ip is in blacklist", zap.String("ip", ip))
			return false
		}
	}
	return true
}

// 数据统一入口
func (d *Dispatch) dataIn(conn wknet.Conn) error {

	if !d.connIsAllow(conn) {
		conn.Close()
		return nil
	}

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
	if !conn.IsAuthed() { // conn is not authed must be connect packet
		packet, size, err := d.s.opts.Proto.DecodeFrame(data, wkproto.LatestVersion)
		if err != nil {
			d.Warn("Failed to decode the message", zap.Error(err))
			conn.Close()
			return nil
		}
		if packet == nil {
			d.Warn("message is nil", zap.ByteString("data", data))
			return nil
		}
		if packet.GetFrameType() != wkproto.CONNECT {
			d.Warn("请先进行连接！")
			conn.Close()
			return nil
		}
		//  process conn auth
		_, _ = conn.Discard(len(data))
		d.processor.processAuth(conn, packet.(*wkproto.ConnectPacket))
		d.s.trace.Metrics.App().ConnPacketCountAdd(1)
		d.s.trace.Metrics.App().ConnPacketBytesAdd(int64(size))
	} else { // authed
		offset := 0
		for len(data) > offset {
			frame, size, err := d.s.opts.Proto.DecodeFrame(data[offset:], uint8(conn.ProtoVersion()))
			if err != nil { //
				d.Warn("Failed to decode the message", zap.Error(err))
				conn.Close()
				return err
			}
			if frame == nil {
				break
			}

			// 统计
			d.s.stats.inMsgs.Add(1)
			d.s.stats.inBytes.Add(int64(size))

			connStats := conn.ConnStats()
			connStats.InPackets.Add(1)
			connStats.InPacketBytes.Add(int64(size))

			switch frame.GetFrameType() {
			case wkproto.SEND:
				connStats.InMsgs.Add(1)
				connStats.InMsgBytes.Add(int64(size))
				d.s.trace.Metrics.App().SendPacketCountAdd(1)
				d.s.trace.Metrics.App().SendPacketBytesAdd(int64(size))
			case wkproto.RECVACK:
				d.s.trace.Metrics.App().RecvackPacketCountAdd(1)
				d.s.trace.Metrics.App().RecvackPacketBytesAdd(int64(size))
			case wkproto.PING:
				d.s.trace.Metrics.App().PingCountAdd(1)
				d.s.trace.Metrics.App().PingBytesAdd(int64(size))

			}

			// context
			connCtx := conn.Context().(*connContext)
			connCtx.putFrame(frame)
			offset += size
		}
		// process frames
		_, _ = conn.Discard(offset)

		d.processor.process(conn)
	}
	return nil
}

// 数据统一出口
func (d *Dispatch) dataOutFrames(conn wknet.Conn, frames ...wkproto.Frame) {
	if len(frames) == 0 {
		return
	}

	// 统计
	connStats := conn.ConnStats()
	d.s.outMsgs.Add(int64(len(frames)))
	connStats.OutPackets.Add(int64(len(frames)))

	wsConn, wsok := conn.(wknet.IWSConn) // websocket连接
	for _, frame := range frames {

		data, err := d.s.opts.Proto.EncodeFrame(frame, uint8(conn.ProtoVersion()))
		if err != nil {
			d.Warn("Failed to encode the message", zap.Error(err))
		} else {
			// 统计
			dataLen := len(data)
			d.s.outBytes.Add(int64(dataLen))

			connStats.OutPacketBytes.Add(int64(dataLen))

			switch frame.GetFrameType() {
			case wkproto.CONNACK:
				d.s.trace.Metrics.App().ConnackPacketCountAdd(1)
				d.s.trace.Metrics.App().ConnackPacketBytesAdd(int64(dataLen))
			case wkproto.RECV:
				connStats.OutMsgs.Add(1)
				connStats.OutMsgBytes.Add(int64(dataLen))
				d.s.trace.Metrics.App().RecvPacketCountAdd(1)
				d.s.trace.Metrics.App().RecvPacketBytesAdd(int64(dataLen))
			case wkproto.SENDACK:
				d.s.trace.Metrics.App().SendackPacketCountAdd(1)
				d.s.trace.Metrics.App().SendackPacketBytesAdd(int64(dataLen))
			case wkproto.PONG:
				d.s.trace.Metrics.App().PongCountAdd(1)
				d.s.trace.Metrics.App().PongBytesAdd(int64(dataLen))
			}

			if wsok {
				err = wsConn.WriteServerBinary(data)
				if err != nil {
					d.Warn("Failed to write the message", zap.Error(err))
				}

			} else {
				_, err = conn.WriteToOutboundBuffer(data)
				if err != nil {
					d.Warn("Failed to write the message", zap.Error(err))
				}
			}

		}
	}
	err := conn.WakeWrite()
	if err != nil {
		d.Warn("Failed to wake write", zap.Error(err), zap.String("uid", conn.UID()), zap.Int("fd", conn.Fd().Fd()), zap.String("deviceId", conn.DeviceID()))
	}

}

// func (d *Dispatch) dataOut(conn wknet.Conn, data []byte) {
// 	if len(data) == 0 {
// 		return
// 	}
// 	wsConn, wsok := conn.(wknet.IWSConn) // websocket连接
// 	if wsok {
// 		err := wsConn.WriteServerBinary(data)
// 		if err != nil {
// 			d.Warn("Failed to write the message", zap.Error(err))
// 		}

// 	} else {
// 		_, err := conn.WriteToOutboundBuffer(data)
// 		if err != nil {
// 			d.Warn("Failed to write the message", zap.Error(err))
// 		}
// 	}
// 	err := conn.WakeWrite()
// 	if err != nil {
// 		d.Warn("Failed to wake write", zap.Error(err))
// 	}
// }

func (d *Dispatch) onConnect(conn wknet.Conn) error {
	conn.SetMaxIdle(time.Second * 2) // 在认证之前，连接最多空闲2秒
	d.s.trace.Metrics.App().ConnCountAdd(1)
	return nil
}

func (d *Dispatch) onClose(conn wknet.Conn) {
	d.Info("conn close for OnClose", zap.Any("conn", conn))

	d.processor.processClose(conn)

	onlineCount, totalOnlineCount := d.s.connManager.GetConnCountWith(conn.UID(), wkproto.DeviceFlag(conn.DeviceFlag())) // 指定的uid和设备下没有新的客户端才算真真的下线（TODO: 有时候离线要比在线晚触发导致不正确）
	d.s.webhook.Offline(conn.UID(), wkproto.DeviceFlag(conn.DeviceFlag()), conn.ID(), onlineCount, totalOnlineCount)     // 触发离线webhook
	if totalOnlineCount == 0 {
		d.s.trace.Metrics.App().OnlineUserCountAdd(-1)
	}
	d.s.trace.Metrics.App().OnlineDeviceCountAdd(-1)

	d.s.trace.Metrics.App().ConnCountAdd(-1)

}

func (d *Dispatch) Start() error {

	d.engine.OnConnect(d.onConnect)
	d.engine.OnData(d.dataIn)
	d.engine.OnClose(d.onClose)

	err := d.engine.Start()
	if err != nil {
		return err
	}
	return err
}

func (d *Dispatch) Stop() error {
	d.Debug("stop...")
	err := d.engine.Stop()
	if err != nil {
		return err
	}
	return err
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
