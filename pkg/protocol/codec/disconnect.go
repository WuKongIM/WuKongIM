package codec

import (
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/pkg/errors"
)

func decodeDisConnect(f frame.Frame, data []byte, version uint8) (frame.Frame, error) {
	dec := NewDecoder(data)
	disConnectPacket := &frame.DisconnectPacket{}
	disConnectPacket.Framer = f.(frame.Framer)
	var err error
	var reasonCode uint8
	if reasonCode, err = dec.Uint8(); err != nil {
		return nil, errors.Wrap(err, "解码reasonCode失败！")
	}
	disConnectPacket.ReasonCode = frame.ReasonCode(reasonCode)

	if disConnectPacket.Reason, err = dec.String(); err != nil {
		return nil, errors.Wrap(err, "解码reason失败！")
	}

	return disConnectPacket, err
}

func encodeDisConnect(disConnectPacket *frame.DisconnectPacket, enc *Encoder, _ uint8) error {
	// 原因代码
	enc.WriteUint8(disConnectPacket.ReasonCode.Byte())
	// 原因
	enc.WriteString(disConnectPacket.Reason)
	return nil
}

func encodeDisConnectSize(packet *frame.DisconnectPacket, _ uint8) int {
	return frame.ReasonCodeByteSize + len(packet.Reason) + frame.StringFixLenByteSize
}
