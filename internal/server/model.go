package server

import (
	"net"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/lmproto"
	"github.com/valyala/bytebufferpool"
)

// conn 连接接口
type conn interface {
	// 获取连接唯一ID
	GetID() uint32
	// 连接的远程地址
	RemoteAddr() net.Addr
	// 写数据
	Write(buf []byte) (n int, err error)
	// 关闭连接
	Close() error
	// Authed 是否已认证
	Authed() bool
	SetAuthed(v bool)
	// Version 协议版本
	Version() uint8
	// SetVersion 设置连接的协议版本
	SetVersion(version uint8)
	SetWriteDeadline(t time.Time) error
	SetReadDeadline(t time.Time) error

	OutboundBuffered() int // OutboundBuffered returns the number of bytes that are queued for writing.
}

type limContext struct {
	frames    []lmproto.Frame
	frameType lmproto.FrameType
	proto     lmproto.Protocol
	s         *Server
	cli       *client
}

func newContext(s *Server) *limContext {
	return &limContext{
		s: s,
	}
}

func (c *limContext) Client() *client {
	return c.cli
}

func (c *limContext) Frames() []lmproto.Frame {

	return c.frames

}

func (c *limContext) GetFrameType() lmproto.FrameType {
	return c.frameType
}

func (c *limContext) AllFrameSize() int {
	if len(c.frames) == 0 {
		return 0
	}
	if len(c.frames) == 1 {
		return int(c.frames[0].GetFrameSize())
	}
	size := 0
	for _, frame := range c.frames {
		size += int(frame.GetFrameSize())
	}
	return size
}

func (c *limContext) reset() {
	c.cli = nil
	c.frames = nil
	c.frameType = 0
}

func (c *limContext) writePacket(frames ...lmproto.Frame) error {
	if len(frames) == 0 {
		return nil
	}
	buffer := bytebufferpool.Get()
	defer bytebufferpool.Put(buffer)
	for _, f := range frames {
		data, err := c.proto.EncodeFrame(f, c.Client().Version())
		if err != nil {
			return err
		}
		buffer.Write(data)
	}

	cli := c.Client()
	if cli != nil {
		cli.outMsgs.Add(int64(len(frames)))
		cli.outBytes.Add(int64(buffer.Len()))
		return cli.Write(buffer.Bytes())
	}
	return nil
}

func (c *limContext) Write(buf []byte) error {
	cli := c.Client()
	if cli != nil {
		return cli.Write(buf)
	}
	return nil
}

type everyScheduler struct {
	Interval time.Duration
}

func (s *everyScheduler) Next(prev time.Time) time.Time {
	return prev.Add(s.Interval)
}
