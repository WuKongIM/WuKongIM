package server

import (
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	"github.com/WuKongIM/WuKongIM/pkg/wkproto"
	"go.uber.org/zap"
)

type Dispatch struct {
	engine    *wknet.Engine
	s         *Server
	processor *Processor
	wklog.Log
	framePool sync.Pool
}

func NewDispatch(s *Server) *Dispatch {
	return &Dispatch{
		engine:    wknet.NewEngine(),
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

// frame data in
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
			// if frame.GetFrameType() == wkproto.PING {
			// 	d.processor.processPing(conn, frame.(*wkproto.PingPacket))
			// }
			conn.Context().(*connContext).putFrame(frame)
			offset += size
		}
		// process frames
		conn.Discard(len(data))
		d.processor.process(conn)
	}
	return nil
}

// frame data out
func (d *Dispatch) dataOut(conn wknet.Conn, frames ...wkproto.Frame) {
	if len(frames) == 0 {
		return
	}
	for _, frame := range frames {
		data, err := d.s.opts.Proto.EncodeFrame(frame, uint8(conn.ProtoVersion()))
		if err != nil {
			d.Warn("Failed to encode the message", zap.Error(err))
		} else {
			_, err = conn.WriteToOutboundBuffer(data)
			if err != nil {
				d.Warn("Failed to write the message", zap.Error(err))
			}
		}
	}
	conn.WakeWrite()

}

func (d *Dispatch) connClose(conn wknet.Conn, err error) {
	d.processor.processClose(conn, err)
}

func (d *Dispatch) Start() error {

	d.engine.OnData(d.dataIn)
	d.engine.OnClose(d.connClose)

	return d.engine.Start()
}

func (d *Dispatch) Stop() error {
	return d.engine.Stop()
}
