package transporter

import (
	"net"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

type Ready struct {
	Req    *CMDReq
	Conn   wknet.Conn
	Result chan []byte
}

type Transporter struct {
	addr   string
	engine *wknet.Engine
	wklog.Log
	processor *processor
	opts      *Options
	stopped   chan struct{}
}

func New(nodeID uint64, addr string, recvChan chan Ready, ops ...Option) *Transporter {

	opts := NewOptions()
	for _, op := range ops {
		op(opts)
	}
	opts.NodeID = nodeID

	engine := wknet.NewEngine(wknet.WithAddr(addr))
	t := &Transporter{
		addr:    addr,
		opts:    opts,
		engine:  engine,
		Log:     wklog.NewWKLog("Transporter"),
		stopped: make(chan struct{}),
	}

	t.processor = newProcessor(recvChan, t, opts)

	return t
}

func (t *Transporter) Start() error {
	t.engine.OnConnect(t.onConnect)
	t.engine.OnClose(t.onClose)
	t.engine.OnData(t.onData)
	err := t.engine.Start()
	if err != nil {
		return err
	}
	return err
}

func (t *Transporter) Stop() {
	err := t.engine.Stop()
	if err != nil {
		t.Warn("stop transporter error", zap.Error(err))
	}
	close(t.stopped)
}

func (t *Transporter) onConnect(conn wknet.Conn) error {

	return nil
}

func (t *Transporter) onClose(conn wknet.Conn) {

}

func (t *Transporter) onData(conn wknet.Conn) error {
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
		packet, _, err := t.opts.proto.DecodeFrame(data, wkproto.LatestVersion)
		if err != nil {
			t.Warn("Failed to decode the message", zap.Error(err))
			conn.Close()
			return nil
		}
		if packet == nil {
			t.Warn("message is nil", zap.ByteString("data", data))
			return nil
		}
		if packet.GetFrameType() != wkproto.CONNECT {
			t.Warn("请先进行连接！")
			conn.Close()
			return nil
		}
		//  process conn auth
		_, err = conn.Discard(len(data))
		if err != nil {
			t.Warn("Failed to discard the message", zap.Error(err))
		}
		t.processor.processAuth(conn, packet.(*wkproto.ConnectPacket))
	} else {
		offset := 0
		for len(data) > offset {
			frame, size, err := t.opts.proto.DecodeFrame(data[offset:], uint8(conn.ProtoVersion()))
			if err != nil { //
				t.Warn("Failed to decode the message", zap.Error(err))
				conn.Close()
				return err
			}
			if frame == nil {
				break
			}
			t.processor.processFrame(conn, frame)
			offset += size
		}
		// process frames
		_, err = conn.Discard(offset)
		if err != nil {
			t.Warn("Failed to discard the message", zap.Error(err))
		}
	}
	return nil
}

func (t *Transporter) Addr() net.Addr {
	return t.engine.TCPRealListenAddr()
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

type OnRecv func(data []byte)
