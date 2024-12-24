package server

import (
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/reactor"
	"github.com/WuKongIM/WuKongIM/internal/track"
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
	var connCtx *reactor.Conn
	connCtxObj := conn.Context()
	if connCtxObj != nil {
		connCtx = connCtxObj.(*reactor.Conn)
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

		connCtx = &reactor.Conn{
			FromNode:     s.opts.Cluster.NodeId,
			ConnId:       conn.ID(),
			Uid:          connectPacket.UID,
			DeviceId:     connectPacket.DeviceID,
			DeviceFlag:   wkproto.DeviceFlag(connectPacket.DeviceFlag),
			ProtoVersion: connectPacket.Version,
			Uptime:       time.Now(),
		}
		conn.SetContext(connCtx)

		conn.SetMaxIdle(time.Second * 4) // 给4秒的时间去认证

		// 如果用户不存在则唤醒用户
		reactor.User.WakeIfNeed(connectPacket.UID)

		// 添加认证
		reactor.User.AddAuth(connCtx, connectPacket)
		_, _ = conn.Discard(len(data))
	} else {
		offset := 0
		var messages []*reactor.UserMessage
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
			if messages == nil {
				messages = make([]*reactor.UserMessage, 0, 10)
			}
			msg := &reactor.UserMessage{
				Frame: frame,
				Conn:  connCtx,
				Track: track.Message{
					PreStart: time.Now(),
				},
			}
			msg.Track.Record(track.PositionStart)
			if frame.GetFrameType() == wkproto.SEND {
				msg.MessageId = options.G.GenMessageId()
				connCtx.InMsgCount.Add(1)
				connCtx.InMsgByteCount.Add(int64(size))
			}

			messages = append(messages, msg)
			offset += size
		}
		if len(messages) > 0 {
			connCtx.InPacketCount.Add(int64(len(messages)))
			connCtx.InPacketByteCount.Add(int64(len(data)))
			// 添加消息
			reactor.User.AddMessages(connCtx.Uid, messages)
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
