package codec

import (
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/pkg/errors"
)

func encodeConnack(connack *frame.ConnackPacket, enc *Encoder, version uint8) error {
	if connack.GetHasServerVersion() {
		enc.WriteUint8(connack.ServerVersion)
	}
	enc.WriteInt64(connack.TimeDiff)
	_ = enc.WriteByte(connack.ReasonCode.Byte())
	enc.WriteString(connack.ServerKey)
	enc.WriteString(connack.Salt)
	if version >= 4 {
		enc.WriteUint64(connack.NodeId)
	}
	return nil
}

func encodeConnackSize(packet *frame.ConnackPacket, version uint8) int {
	size := 0
	if packet.GetHasServerVersion() {
		size += frame.VersionByteSize
	}
	size += frame.TimeDiffByteSize
	size += frame.ReasonCodeByteSize
	size += len(packet.ServerKey) + frame.StringFixLenByteSize
	size += len(packet.Salt) + frame.StringFixLenByteSize
	if version >= 4 {
		size += frame.NodeIdByteSize
	}
	return size
}

func decodeConnack(f frame.Frame, data []byte, version uint8) (frame.Frame, error) {
	dec := NewDecoder(data)
	connackPacket := &frame.ConnackPacket{}
	connackPacket.Framer = f.(frame.Framer)

	var err error

	if f.GetHasServerVersion() {
		if connackPacket.ServerVersion, err = dec.Uint8(); err != nil {
			return nil, errors.Wrap(err, "解码version失败！")
		}
	}

	if connackPacket.TimeDiff, err = dec.Int64(); err != nil {
		return nil, errors.Wrap(err, "解码TimeDiff失败！")
	}
	var reasonCode uint8
	if reasonCode, err = dec.Uint8(); err != nil {
		return nil, errors.Wrap(err, "解码ReasonCode失败！")
	}
	connackPacket.ReasonCode = frame.ReasonCode(reasonCode)

	if connackPacket.ServerKey, err = dec.String(); err != nil {
		return nil, errors.Wrap(err, "解码ServerKey失败！")
	}
	if connackPacket.Salt, err = dec.String(); err != nil {
		return nil, errors.Wrap(err, "解码Salt失败！")
	}

	if version >= 4 {
		if connackPacket.NodeId, err = dec.Uint64(); err != nil {
			return nil, errors.Wrap(err, "解码NodeId失败！")
		}
	}

	return connackPacket, nil
}
