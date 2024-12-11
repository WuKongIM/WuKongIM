package proto

import (
	"encoding/binary"
	"fmt"

	"github.com/panjf2000/gnet/v2"
)

// Protocol format:
//
// * 0             1                     5
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
	Encode(data []byte, msgType uint8) ([]byte, error)
}

type DefaultProto struct {
}

func New() *DefaultProto {

	return &DefaultProto{}
}

func (d *DefaultProto) Decode(c gnet.Conn) ([]byte, MsgType, int, error) {

	msgByteBuff, err := c.Peek(1)
	if err != nil {
		return nil, 0, 0, err
	}

	msgType := uint8(msgByteBuff[0])
	if msgType == MsgTypeHeartbeat.Uint8() {
		_, _ = c.Discard(1)
		return []byte{MsgTypeHeartbeat.Uint8()}, MsgTypeHeartbeat, MsgTypeLength, nil
	}

	minSize := MsgTypeLength + MsgContentLength

	contentLenBytes, err := c.Peek(MsgTypeLength + MsgContentLength)
	if err != nil {
		return nil, 0, 0, err
	}

	contentLen := binary.BigEndian.Uint32(contentLenBytes[MsgTypeLength:])

	buf, err := c.Peek(int(contentLen) + minSize)
	if err != nil {
		return nil, 0, 0, err
	}

	contentBytes := make([]byte, contentLen)
	copy(contentBytes, buf[minSize:])

	msgLen := minSize + int(contentLen)

	_, _ = c.Discard(msgLen)

	return contentBytes, MsgType(msgType), msgLen, nil
}

func (d *DefaultProto) Encode(data []byte, msgType uint8) ([]byte, error) {

	msgOffset := MsgTypeLength + MsgContentLength
	msgLen := msgOffset + len(data)

	msgData := make([]byte, msgLen)
	copy(msgData, []byte{msgType})
	binary.BigEndian.PutUint32(msgData[MsgTypeLength:msgOffset], uint32(len(data)))
	copy(msgData[msgOffset:msgLen], data)

	return msgData, nil
}
