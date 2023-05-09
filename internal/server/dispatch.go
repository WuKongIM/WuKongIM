package server

import (
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/limlog"
	"github.com/WuKongIM/WuKongIM/pkg/limnet"
	"github.com/WuKongIM/WuKongIM/pkg/lmproto"
	"go.uber.org/zap"
)

type Dispatch struct {
	engine    *limnet.Engine
	s         *Server
	processor *Processor
	limlog.Log
	framePool sync.Pool
}

func NewDispatch(s *Server) *Dispatch {
	return &Dispatch{
		engine:    limnet.NewEngine(),
		s:         s,
		processor: NewProcessor(s),
		Log:       limlog.NewLIMLog("Dispatch"),
		framePool: sync.Pool{
			New: func() any {
				return make([]lmproto.Frame, 20)
			},
		},
	}
}

// frame data in
func (d *Dispatch) dataIn(conn limnet.Conn) error {
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
		packet, _, err := d.s.opts.Proto.DecodeFrame(data, lmproto.LatestVersion)
		if err != nil {
			d.Warn("Failed to decode the message", zap.Error(err))
			conn.Close()
			return nil
		}
		if packet.GetFrameType() != lmproto.CONNECT {
			d.Warn("请先进行连接！")
			conn.Close()
			return nil
		}
		//  process conn auth
		conn.Discard(len(data))
		d.processor.processAuth(conn, packet.(*lmproto.ConnectPacket))
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
func (d *Dispatch) dataOut(conn limnet.Conn, frames ...lmproto.Frame) {
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

func (d *Dispatch) connClose(conn limnet.Conn, err error) {
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
