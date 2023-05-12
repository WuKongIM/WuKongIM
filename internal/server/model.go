package server

import (
	"net"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkproto"
	"github.com/WuKongIM/WuKongIM/pkg/wkstore"
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
	frames    []wkproto.Frame
	frameType wkproto.FrameType
	proto     wkproto.Protocol
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

func (c *limContext) Frames() []wkproto.Frame {

	return c.frames

}

func (c *limContext) GetFrameType() wkproto.FrameType {
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

func (c *limContext) writePacket(frames ...wkproto.Frame) error {
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

type Message struct {
	wkproto.RecvPacket
	ToUID          string             // 接受者
	Subscribers    []string           // 订阅者 如果此字段有值 则表示消息只发送给指定的订阅者
	fromDeviceFlag wkproto.DeviceFlag // 发送者设备标示
	fromDeviceID   string             // 发送者设备ID
	// 重试相同的clientID
	toClientID int64 // 指定接收客户端的ID
	large      bool  // 是否是超大群
	// ------- 优先队列用到 ------
	index      int   //在切片中的索引值
	pri        int64 // 优先级的时间点 值越小越优先
	retryCount int   // 当前重试次数
}

func (m *Message) SetSeq(seq uint32) {
	m.MessageSeq = seq
}

func (m *Message) GetSeq() uint32 {
	return m.MessageSeq
}

func (m *Message) Encode() []byte {
	var version uint8 = 0
	data := MarshalMessage(version, m)
	return wkstore.EncodeMessage(m.MessageSeq, data)
}

func (m *Message) Decode(msg []byte) error {
	messageSeq, data, err := wkstore.DecodeMessage(msg)
	if err != nil {
		return err
	}
	err = UnmarshalMessage(data, m)
	m.MessageSeq = messageSeq
	return err
}

// MarshalMessage MarshalMessage
func MarshalMessage(version uint8, m *Message) []byte {
	enc := wkproto.NewEncoder()
	enc.WriteByte(wkproto.ToFixHeaderUint8(&m.RecvPacket))
	enc.WriteUint8(version)
	enc.WriteByte(m.Setting.Uint8())
	enc.WriteInt64(m.MessageID)
	enc.WriteUint32(m.MessageSeq)
	enc.WriteString(m.ClientMsgNo)
	enc.WriteInt32(m.Timestamp)
	enc.WriteString(m.FromUID)
	enc.WriteString(m.ChannelID)
	enc.WriteUint8(m.ChannelType)
	enc.WriteBytes(m.Payload)
	return enc.Bytes()
}

// UnmarshalMessage UnmarshalMessage
func UnmarshalMessage(data []byte, m *Message) error {
	dec := wkproto.NewDecoder(data)

	// header
	var err error
	var header uint8
	if header, err = dec.Uint8(); err != nil {
		return err
	}
	recvPacket := wkproto.RecvPacket{}
	framer := wkproto.FramerFromUint8(header)
	if _, err = dec.Uint8(); err != nil {
		return err
	}
	recvPacket.Framer = framer

	// setting
	var setting uint8
	if setting, err = dec.Uint8(); err != nil {
		return err
	}
	m.RecvPacket = recvPacket

	m.Setting = wkproto.Setting(setting)

	// messageID
	if m.MessageID, err = dec.Int64(); err != nil {
		return err
	}

	// MessageSeq
	if m.MessageSeq, err = dec.Uint32(); err != nil {
		return err
	}
	// ClientMsgNo
	if m.ClientMsgNo, err = dec.String(); err != nil {
		return err
	}
	// Timestamp
	if m.Timestamp, err = dec.Int32(); err != nil {
		return err
	}

	// FromUID
	if m.FromUID, err = dec.String(); err != nil {
		return err
	}
	// if m.QueueUID, err = dec.String(); err != nil {
	// 	return err
	// }

	// ChannelID
	if m.ChannelID, err = dec.String(); err != nil {
		return err
	}

	// ChannelType
	if m.ChannelType, err = dec.Uint8(); err != nil {
		return err
	}

	// Payload
	if m.Payload, err = dec.BinaryAll(); err != nil {
		return err
	}
	return nil
}
