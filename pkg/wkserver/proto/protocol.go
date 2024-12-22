package proto

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/panjf2000/gnet/v2"
	"go.uber.org/zap"
)

// Protocol format:
//
// * 0             1                     5
// * +-------------+---------------------+
// * |        magic  number  start       |
// * +-------------+---------------------+
// * |   msg type  |        data len     |
// * +-----------+-----------+-----------+
// * |                                   |
// * +                                   +
// * |           data bytes              |
// * +                                   +
// * |            ... ...                |
// * +-----------------------------------+

type MsgType uint8 // 消息类型
const (
	Unknown          MsgType = iota
	MsgTypeConnect           // connect
	MsgTypeConnack           // connack
	MsgTypeRequest           // request
	MsgTypeResp              // response
	MsgTypeHeartbeat         // heartbeat
	MsgTypeMessage           // message
)

const (
	MsgTypeLength    = 1
	MsgContentLength = 4
)

var (
	// MagicNumberStart 协议包开始标志 wukongim
	MagicNumberStart       = []byte{'W', 'U', 'K', 'O', 'N', 'G'}
	MagicNumberStartLength = len(MagicNumberStart)
)

func (m MsgType) Uint8() uint8 {
	return uint8(m)
}

func (m MsgType) String() string {
	switch m {
	case MsgTypeConnect:
		return "MsgTypeConnect"
	case MsgTypeConnack:
		return "MsgTypeConnack"
	case MsgTypeRequest:
		return "MsgTypeRequest"
	case MsgTypeResp:
		return "MsgTypeResp"
	case MsgTypeHeartbeat:
		return "MsgTypeHeartbeat"
	case MsgTypeMessage:
		return "MsgTypeMessage"
	default:
		return fmt.Sprintf("Unknown MsgType %d", m)
	}
}

type Protocol interface {
	Decode(c gnet.Conn) ([]byte, MsgType, int, error)
	Encode(data []byte, msgType MsgType) ([]byte, error)
}

type DefaultProto struct {
	wklog.Log
}

func New() *DefaultProto {

	return &DefaultProto{
		Log: wklog.NewWKLog("wkserver.proto"),
	}
}

func (d *DefaultProto) Decode(c gnet.Conn) ([]byte, MsgType, int, error) {
	// 最小消息长度 = MagicNumberStart + MsgType + MsgContentLength + MagicNumberEnd
	minSize := MagicNumberStartLength + MsgTypeLength
	if c.InboundBuffered() < minSize {
		return nil, 0, 0, io.ErrShortBuffer
	}

	// magic number start
	magicStart, err := c.Peek(MagicNumberStartLength)
	if err != nil {
		return nil, 0, 0, err
	}

	if !bytes.Equal(magicStart, MagicNumberStart) {
		d.Error("decode: invalid magic number start", zap.ByteString("act", magicStart), zap.ByteString("expect", MagicNumberStart), zap.Int("totalLen", c.InboundBuffered()))
		return nil, 0, 0, fmt.Errorf("invalid magic number start")
	}

	// 读取消息类型
	msgByteBuff, err := c.Peek(MagicNumberStartLength + MsgTypeLength)
	if err != nil {
		return nil, 0, 0, err
	}
	msgType := uint8(msgByteBuff[MagicNumberStartLength])
	// 如果是心跳消息，直接返回
	if msgType == MsgTypeHeartbeat.Uint8() {
		_, _ = c.Discard(MagicNumberStartLength + MsgTypeLength)
		return []byte{MsgTypeHeartbeat.Uint8()}, MsgTypeHeartbeat, MagicNumberStartLength + MsgTypeLength, nil
	}

	// 读取数据长度字段
	contentLenBytes, err := c.Peek(MagicNumberStartLength + MsgTypeLength + MsgContentLength)
	if err != nil {
		return nil, 0, 0, err
	}

	// 从内容长度字段中提取数据长度
	contentLen := binary.BigEndian.Uint32(contentLenBytes[MagicNumberStartLength+MsgTypeLength:])

	// 读取整个消息数据（包括内容和结束魔数）
	totalSize := MagicNumberStartLength + MsgTypeLength + MsgContentLength + int(contentLen)
	buf, err := c.Peek(totalSize)
	if err != nil {
		return nil, 0, 0, err
	}

	// 提取实际内容
	contentBytes := make([]byte, contentLen)
	copy(contentBytes, buf[MagicNumberStartLength+MsgTypeLength+MsgContentLength:totalSize])

	// 计算总消息长度
	msgLen := totalSize

	// 丢弃已读取的字节
	_, err = c.Discard(msgLen)
	if err != nil {
		d.Warn("discard error", zap.Error(err))
	}

	return contentBytes, MsgType(msgType), msgLen, nil
}

func (d *DefaultProto) Encode(data []byte, msgType MsgType) ([]byte, error) {
	if msgType == MsgTypeHeartbeat {
		msgData := make([]byte, MagicNumberStartLength+MsgTypeLength)
		copy(msgData, MagicNumberStart)
		msgData[MagicNumberStartLength] = msgType.Uint8()
		return msgData, nil
	}

	msgContentOffset := MsgTypeLength + MsgContentLength + len(MagicNumberStart)
	msgLen := msgContentOffset + len(data)

	msgData := make([]byte, msgLen)

	// magic number start
	copy(msgData, MagicNumberStart)
	// msgType
	copy(msgData[MagicNumberStartLength:MagicNumberStartLength+1], []byte{msgType.Uint8()})
	// data len
	binary.BigEndian.PutUint32(msgData[MagicNumberStartLength+MsgTypeLength:msgContentOffset], uint32(len(data)))
	// data
	copy(msgData[msgContentOffset:msgLen], data)

	return msgData, nil
}
