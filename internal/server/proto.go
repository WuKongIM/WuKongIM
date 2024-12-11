package server

import (
	"strings"

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
	var connCtx *connContext
	connCtxObj := conn.Context()
	if connCtxObj != nil {
		connCtx = connCtxObj.(*connContext)
		isAuth = connCtx.isAuth.Load()
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
		if IsSpecialChar(connectPacket.UID) {
			s.Warn("UID is illegal,conn will be closed", zap.String("uid", connectPacket.UID))
			conn.Close()
			return nil
		}

		sub := s.userReactor.reactorSub(connectPacket.UID)
		connInfo := connInfo{
			connId:       conn.ID(),
			uid:          connectPacket.UID,
			deviceId:     connectPacket.DeviceID,
			deviceFlag:   wkproto.DeviceFlag(connectPacket.DeviceFlag),
			protoVersion: connectPacket.Version,
		}
		connCtx = newConnContext(connInfo, conn, sub)
		conn.SetContext(connCtx)

		// 添加用户的连接，如果用户不存在则创建
		s.userReactor.addConnAndCreateUserHandlerIfNotExist(connCtx)

		connCtx.addConnectPacket(connectPacket)

		_, _ = conn.Discard(len(data))
	} else {
		offset := 0
		for len(data) > offset {
			frame, size, err := s.opts.Proto.DecodeFrame(data[offset:], connCtx.protoVersion)
			if err != nil { //
				s.Warn("Failed to decode the message", zap.Error(err))
				conn.Close()
				return err
			}
			if frame == nil {
				break
			}
			offset += size
			if frame.GetFrameType() == wkproto.SEND {
				sendPacket := frame.(*wkproto.SendPacket)

				connCtx.addSendPacket(sendPacket)
			} else {
				connCtx.addOtherPacket(frame)
			}
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
