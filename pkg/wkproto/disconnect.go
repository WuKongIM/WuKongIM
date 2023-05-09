package wkproto

import (
	"fmt"

	"github.com/pkg/errors"
)

// DisconnectPacket 断开连接数据包
type DisconnectPacket struct {
	Framer
	ReasonCode ReasonCode // 断开原因代号
	Reason     string     // 断开原因
}

// GetFrameType 包类型
func (c DisconnectPacket) GetFrameType() FrameType {
	return DISCONNECT
}

func (c DisconnectPacket) String() string {
	return fmt.Sprintf("ReasonCode:%d Reason:%s", c.ReasonCode, c.Reason)
}

func decodeDisConnect(frame Frame, data []byte, version uint8) (Frame, error) {
	dec := NewDecoder(data)
	disConnectPacket := &DisconnectPacket{}
	disConnectPacket.Framer = frame.(Framer)
	var err error
	var reasonCode uint8
	if reasonCode, err = dec.Uint8(); err != nil {
		return nil, errors.Wrap(err, "解码reasonCode失败！")
	}
	disConnectPacket.ReasonCode = ReasonCode(reasonCode)

	if disConnectPacket.Reason, err = dec.String(); err != nil {
		return nil, errors.Wrap(err, "解码reason失败！")
	}

	return disConnectPacket, err
}

func encodeDisConnect(disConnectPacket *DisconnectPacket, enc *Encoder, version uint8) error {
	// 原因代码
	enc.WriteUint8(disConnectPacket.ReasonCode.Byte())
	// 原因
	enc.WriteString(disConnectPacket.Reason)
	return nil
}

func encodeDisConnectSize(packet *DisconnectPacket, version uint8) int {

	return ReasonCodeByteSize + len(packet.Reason) + StringFixLenByteSize
}
