package server

import (
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	"github.com/WuKongIM/WuKongIM/pkg/wkproto"
	"github.com/gobwas/ws/wsutil"
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
		engine:    wknet.NewEngine(wknet.WithAddr(s.opts.Addr), wknet.WithWSAddr(s.opts.WSAddr)),
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

// 数据统一入口
func (d *Dispatch) dataIn(conn wknet.Conn) error {
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
		packet, _, err := d.s.opts.Proto.DecodeFrame(data, wkproto.LatestVersion)
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
		conn.Discard(len(data))
		d.processor.processAuth(conn, packet.(*wkproto.ConnectPacket))
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

			connCtx := conn.Context().(*connContext)
			connCtx.inBytes.Add(int64(len(data)))
			connCtx.putFrame(frame)
			offset += size
		}
		// process frames
		conn.Discard(offset)

		d.processor.process(conn)
	}
	return nil
}

// 数据统一出口
func (d *Dispatch) dataOut(conn wknet.Conn, frames ...wkproto.Frame) {
	if len(frames) == 0 {
		return
	}

	connCtx, hasConnCtx := conn.Context().(*connContext)
	if hasConnCtx {
		connCtx.outMsgs.Add(int64(len(frames)))
	}

	for _, frame := range frames {
		data, err := d.s.opts.Proto.EncodeFrame(frame, uint8(conn.ProtoVersion()))
		if err != nil {
			d.Warn("Failed to encode the message", zap.Error(err))
		} else {
			if hasConnCtx {
				connCtx.outBytes.Add(int64(len(data)))
			}

			wsConn, ok := conn.(*wknet.WSConn) // websocket连接
			if ok {
				err = wsutil.WriteServerBinary(wsConn, data) // TODO: 有优化空间
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
	conn.WakeWrite()

}

func (d *Dispatch) connClose(conn wknet.Conn) {
	d.Debug("conn close for OnClose", zap.Any("conn", conn))
	d.s.connManager.RemoveConn(conn)
	d.processor.processClose(conn)
}

func (d *Dispatch) Start() error {

	d.engine.OnData(d.dataIn)
	d.engine.OnClose(d.connClose)

	err := d.engine.Start()
	if err != nil {
		return err
	}
	return err
}

func (d *Dispatch) Stop() error {
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
