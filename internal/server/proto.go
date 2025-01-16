package server

import (
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/eventbus"
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/track"
	"github.com/WuKongIM/WuKongIM/pkg/fasttime"
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

func (s *Server) onData(conn wknet.Conn) error {
	buff, err := conn.Peek(-1)
	if err != nil {
		return err
	}
	if len(buff) == 0 {
		return nil
	}

	var isAuth bool
	var connCtx *eventbus.Conn
	connCtxObj := conn.Context()
	if connCtxObj != nil {
		connCtx = connCtxObj.(*eventbus.Conn)
		isAuth = connCtx.Auth
	} else {
		isAuth = false
	}

	if !isAuth {
		if isProxyProto(buff) { // 是否是代理协议
			remoteAddr, size, err := parseProxyProto(buff)
			if err != nil && err != ErrNoProxyProtocol {
				s.Warn("Failed to parse proxy proto", zap.Error(err))
			}
			if remoteAddr != nil {
				conn.SetRemoteAddr(remoteAddr)
				s.Debug("parse proxy proto success", zap.String("remoteAddr", remoteAddr.String()))
			}
			if size > 0 {
				_, _ = conn.Discard(size)
				buff = buff[size:]
			}
		}
	}

	// 解码协议包
	data, _ := gnetUnpacket(buff)
	if len(data) == 0 {
		return nil
	}

	if !isAuth {

		// 解析连接包
		packet, _, err := s.opts.Proto.DecodeFrame(data, wkproto.LatestVersion)
		if err != nil {
			s.Warn("Failed to decode the message,conn will be closed", zap.Error(err))
			conn.Close()
			return nil
		}
		if packet == nil {
			s.Warn("packet is nil,conn will be closed", zap.ByteString("data", data))
			conn.Close()
			return nil
		}
		if packet.GetFrameType() != wkproto.CONNECT {
			s.Warn("请先进行连接！")
			conn.Close()
			return nil
		}
		connectPacket := packet.(*wkproto.ConnectPacket)

		if strings.TrimSpace(connectPacket.UID) == "" {
			s.Warn("UID is empty,conn will be closed")
			conn.Close()
			return nil
		}
		if options.IsSpecialChar(connectPacket.UID) {
			s.Warn("UID is illegal,conn will be closed", zap.String("uid", connectPacket.UID))
			conn.Close()
			return nil
		}

		connCtx = &eventbus.Conn{
			NodeId:       s.opts.Cluster.NodeId,
			ConnId:       conn.ID(),
			Uid:          connectPacket.UID,
			DeviceId:     connectPacket.DeviceID,
			DeviceFlag:   wkproto.DeviceFlag(connectPacket.DeviceFlag),
			ProtoVersion: connectPacket.Version,
			Uptime:       fasttime.UnixTimestamp(),
		}
		conn.SetContext(connCtx)

		conn.SetMaxIdle(time.Second * 4) // 给4秒的时间去认证

		// 添加连接事件
		eventbus.User.Connect(connCtx, connectPacket)
		eventbus.User.Advance(connCtx.Uid)

		_, _ = conn.Discard(len(data))
	} else {
		offset := 0
		var events []*eventbus.Event
		for len(data) > offset {
			frame, size, err := s.opts.Proto.DecodeFrame(data[offset:], connCtx.ProtoVersion)
			if err != nil { //
				s.Warn("Failed to decode the message", zap.Error(err))
				conn.Close()
				return err
			}
			if frame == nil {
				break
			}
			if events == nil {
				events = make([]*eventbus.Event, 0, 10)
			}
			event := &eventbus.Event{
				Type:         eventbus.EventOnSend,
				Frame:        frame,
				Conn:         connCtx,
				SourceNodeId: options.G.Cluster.NodeId,
				Track: track.Message{
					PreStart: time.Now(),
				},
			}
			event.Track.Record(track.PositionStart)
			// 生成messageId
			if frame.GetFrameType() == wkproto.SEND {
				event.MessageId = options.G.GenMessageId()
			}

			events = append(events, event)
			offset += size
		}
		if len(events) > 0 {
			// 添加事件
			eventbus.User.AddEvents(connCtx.Uid, events)

		}

		_, _ = conn.Discard(offset)
	}

	return nil
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
