package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/eventbus"
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/track"
	"github.com/WuKongIM/WuKongIM/pkg/fasttime"
	"github.com/WuKongIM/WuKongIM/pkg/jsonrpc"
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/pkg/errors"
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
	var isJson = false             // 是否是jsonrpc请求
	if !options.G.DisableJSONRPC { // 如果没有禁用jsonrpc，则判断是否是jsonrpc请求
		isJson = jsonrpc.IsJSONObjectPrefix(buff)
	} else {
		isJson = false
	}
	if !isAuth {
		var consumedBytes int
		connCtx, consumedBytes, err = s.handleUnauthenticatedConn(conn, buff, isJson)
		if err != nil {
			return err
		}
		if connCtx == nil {
			return nil
		}
		_, _ = conn.Discard(consumedBytes)
		return nil
	} else {
		err = s.handleAuthenticatedConn(conn, connCtx, buff, isJson)
		// 如果jsonrpc请求，则不返回错误, 因为jsonrpc请求将错误返回给客户端了，这里不返回error是为了防止返回error服务将此连接关闭
		if isJson {
			if err != nil {
				s.Warn("Failed to handle authenticated conn", zap.Error(err))
				return nil
			}
		}
		return err
	}
}

// handleAuthenticatedConn handles data frames received on an already authenticated connection.
// It decodes frames in a loop, creates events for them, and publishes the events.
// It discards the processed bytes from the connection buffer.
func (s *Server) handleAuthenticatedConn(conn wknet.Conn, connCtx *eventbus.Conn, buff []byte, isJson bool) error {
	offset := 0
	var events []*eventbus.Event

	frames := make([]wkproto.Frame, 0, 10)
	reqIds := make([]string, 0, 10)
	if isJson {
		reader := bytes.NewReader(buff)
		decoder := json.NewDecoder(reader)
		for {
			packet, probe, err := jsonrpc.Decode(decoder)
			if err != nil {
				if err == io.EOF {
					break
				}
				s.Warn("Failed to decode jsonrpc packet", zap.Error(err))
				eventbus.User.WriteLocalData(connCtx, jsonrpc.EncodeErrorResponse(jsonrpc.DecodeID(probe.ID), err))
				return err
			}
			if packet == nil {
				break
			}
			frame, reqId, err := jsonrpc.ToFrame(packet)
			if err != nil {
				s.Warn("Failed to convert jsonrpc packet to frame", zap.Error(err))
				eventbus.User.WriteLocalData(connCtx, jsonrpc.EncodeErrorResponse(jsonrpc.DecodeID(probe.ID), err))
				return err
			}
			frames = append(frames, frame)
			reqIds = append(reqIds, reqId)
		}
		offset += (len(buff) - reader.Len())

	} else {
		for len(buff) > offset {
			frameData, _ := unpacket(buff[offset:])
			if len(frameData) == 0 {
				break
			}
			frame, size, err := s.opts.Proto.DecodeFrame(frameData, connCtx.ProtoVersion)
			if err != nil { // Decoding error on subsequent frames
				s.Warn("Failed to decode subsequent frame", zap.Error(err), zap.String("uid", connCtx.Uid))
				conn.Close() // Close connection on bad data
				return err
			}
			if frame == nil {
				// Not enough data for a complete frame in the current buffer
				break
			}
			frames = append(frames, frame)
			offset += size
		}
	}

	if len(frames) == 0 {
		return nil
	}

	for i, frame := range frames {

		var reqId string
		if len(reqIds) > 0 && len(reqIds) == len(frames) {
			reqId = reqIds[i]
		}

		event := &eventbus.Event{
			Type:         eventbus.EventOnSend, // Assuming all data frames trigger an OnSend event internally
			Frame:        frame,
			Conn:         connCtx,
			SourceNodeId: options.G.Cluster.NodeId,
			Track: track.Message{
				PreStart: time.Now(),
			},
			ReqId: reqId,
		}
		event.Track.Record(track.PositionStart)

		// Generate messageId for SEND frames
		if frame.GetFrameType() == wkproto.SEND {
			event.MessageId = options.G.GenMessageId()
		}

		events = append(events, event)
	}
	// Publish collected events
	if len(events) > 0 {
		eventbus.User.AddEvents(connCtx.Uid, events)
	}

	// Discard the processed bytes from the connection buffer
	if offset > 0 {
		_, _ = conn.Discard(offset)
	}

	return nil // Success
}

// handleUnauthenticatedConn handles the initial message(s) from a connection
// that has not yet been authenticated. It expects a CONNECT packet, potentially
// preceded by a PROXY protocol header.
// It parses the proxy info, validates the CONNECT packet, creates the connection context,
// publishes events, and sets the initial idle timeout.
// Returns the created connection context, the number of bytes consumed from the buffer,
// and an error if any step fails.
func (s *Server) handleUnauthenticatedConn(conn wknet.Conn, buff []byte, isJson bool) (ctx *eventbus.Conn, consumedBytes int, err error) {
	dataOffset := 0

	// 1. Check for and parse PROXY protocol header
	if isProxyProto(buff) {
		remoteAddr, size, proxyErr := parseProxyProto(buff)
		if proxyErr != nil && proxyErr != ErrNoProxyProtocol {
			s.Warn("Failed to parse proxy proto", zap.Error(proxyErr))
		}
		if remoteAddr != nil {
			conn.SetRemoteAddr(remoteAddr)
			s.Debug("PROXY protocol: updated remote address", zap.String("remoteAddr", remoteAddr.String()))
		}
		if size > 0 {
			s.Debug("PROXY protocol: discarding header", zap.Int("size", size))
			buff = buff[size:]
			dataOffset += size
		}
	}

	// 2. Decode the first frame (must be CONNECT)
	var connectPacket *wkproto.ConnectPacket
	var reqId string // 请求id (jsonrpc)
	// 如果buff是jsonrpc请求，则解包
	if isJson && !options.G.DisableJSONRPC {
		reader := bytes.NewReader(buff)
		decoder := json.NewDecoder(reader)
		packet, probe, err := jsonrpc.Decode(decoder)
		if err != nil {
			s.Warn("Failed to decode jsonrpc packet", zap.Error(err))
			conn.Close()
			return nil, 0, err
		}
		if probe.Method != jsonrpc.MethodConnect {
			s.Warn("First frame is not CONNECT, closing connection", zap.String("frameType", probe.Method))
			conn.Close()
			return nil, 0, fmt.Errorf("expected CONNECT frame, got %s", probe.Method)
		}
		connectReq := packet.(jsonrpc.ConnectRequest)
		connectPacket = connectReq.Params.ToProto()
		reqId = connectReq.ID
		consumedBytes = dataOffset + (len(buff) - reader.Len())
	} else {
		frameData, frameType := unpacket(buff)
		if len(frameData) == 0 {
			return nil, 0, nil
		}

		if frameType != wkproto.CONNECT {
			s.Warn("frameType is not CONNECT, closing connection", zap.String("frameType", frameType.String()), zap.ByteString("data", frameData))
			conn.Close()
			return
		}

		packet, firstFrameSize, decodeErr := s.opts.Proto.DecodeFrame(frameData, wkproto.LatestVersion)
		if decodeErr != nil {
			s.Warn("Failed to decode first frame, closing connection", zap.Error(decodeErr), zap.ByteString("data", frameData))
			conn.Close()
			return nil, 0, decodeErr
		}
		if packet == nil {
			s.Warn("Decoded first frame is nil, closing connection", zap.ByteString("data", frameData))
			conn.Close()
			return nil, 0, errors.New("decoded nil frame")
		}

		consumedBytes = dataOffset + firstFrameSize

		if packet.GetFrameType() != wkproto.CONNECT {
			s.Warn("First frame is not CONNECT, closing connection", zap.String("frameType", packet.GetFrameType().String()))
			conn.Close()
			return nil, consumedBytes, fmt.Errorf("expected CONNECT frame, got %s", packet.GetFrameType().String())
		}
		var ok bool
		connectPacket, ok = packet.(*wkproto.ConnectPacket)
		if !ok {
			s.Warn("Could not assert frame as ConnectPacket, closing connection")
			conn.Close()
			return nil, consumedBytes, errors.New("failed to assert frame as ConnectPacket")
		}
	}

	if strings.TrimSpace(connectPacket.UID) == "" {
		s.Warn("CONNECT packet UID is empty, closing connection")
		conn.Close()
		return nil, consumedBytes, errors.New("connect packet UID is empty")
	}
	if options.IsSpecialChar(connectPacket.UID) {
		s.Warn("CONNECT packet UID is illegal, closing connection", zap.String("uid", connectPacket.UID))
		conn.Close()
		return nil, consumedBytes, errors.New("connect packet UID is illegal")
	}

	connCtx := &eventbus.Conn{
		NodeId:       s.opts.Cluster.NodeId,
		ConnId:       conn.ID(),
		Uid:          connectPacket.UID,
		DeviceId:     connectPacket.DeviceID,
		DeviceFlag:   wkproto.DeviceFlag(connectPacket.DeviceFlag),
		ProtoVersion: connectPacket.Version,
		Uptime:       fasttime.UnixTimestamp(),
		IsJsonRpc:    isJson,
	}
	conn.SetContext(connCtx)

	conn.SetMaxIdle(time.Second * 4)

	eventbus.User.Connect(reqId, connCtx, connectPacket)
	eventbus.User.Advance(connCtx.Uid)

	return connCtx, consumedBytes, nil
}

func unpacket(buff []byte) ([]byte, wkproto.FrameType) {
	if len(buff) <= 0 {
		return nil, 0
	}
	offset := 0 // 偏移位置
	var packetType wkproto.FrameType
	for len(buff) > offset {
		typeAndFlags := buff[offset]
		packetType = wkproto.FrameType(typeAndFlags >> 4)
		if packetType == wkproto.PING || packetType == wkproto.PONG {
			offset++
			continue
		}
		// 解码剩余长度 reminLen 和 读取长度 readSize
		reminLen, readSize, has := decodeLength(buff[offset+1:])
		if !has {
			break
		}
		dataEnd := offset + readSize + reminLen + 1
		if len(buff) >= dataEnd {
			offset = dataEnd
			continue
		} else {
			break
		}
	}

	if offset > 0 {
		return buff[:offset], packetType
	}

	return nil, packetType
}

func decodeLength(data []byte) (int, int, bool) {
	var rLength uint32
	var multiplier uint32
	offset := 0
	for multiplier < 27 {
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
