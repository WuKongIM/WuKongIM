package codec

import (
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/pkg/errors"
)

func decodeRecv(f frame.Frame, data []byte, version uint8) (frame.Frame, error) {
	dec := NewDecoder(data)
	recvPacket := &frame.RecvPacket{}
	recvPacket.Framer = f.(frame.Framer)
	var err error
	setting, err := dec.Uint8()
	if err != nil {
		return nil, errors.Wrap(err, "解码消息设置失败！")
	}
	recvPacket.Setting = frame.Setting(setting)
	// MsgKey
	if recvPacket.MsgKey, err = dec.String(); err != nil {
		return nil, errors.Wrap(err, "解码MsgKey失败！")
	}
	// 发送者
	if recvPacket.FromUID, err = dec.String(); err != nil {
		return nil, errors.Wrap(err, "解码FromUID失败！")
	}
	// 频道ID
	if recvPacket.ChannelID, err = dec.String(); err != nil {
		return nil, errors.Wrap(err, "解码ChannelId失败！")
	}
	// 频道类型
	if recvPacket.ChannelType, err = dec.Uint8(); err != nil {
		return nil, errors.Wrap(err, "解码ChannelType失败！")
	}
	if version >= 3 {
		var expire uint32
		if expire, err = dec.Uint32(); err != nil {
			return nil, errors.Wrap(err, "解码Expire失败！")
		}
		recvPacket.Expire = expire
	}
	// 客户端唯一标示
	if recvPacket.ClientMsgNo, err = dec.String(); err != nil {
		return nil, errors.Wrap(err, "解码ClientMsgNo失败！")
	}
	// 流消息
	if version < 5 { // 5版本后不再支持recv里不再需要streamNo和streamId
		if version >= 2 && recvPacket.Setting.IsSet(frame.SettingStream) {
			var streamFlag uint8
			if streamFlag, err = dec.Uint8(); err != nil {
				return nil, errors.Wrap(err, "解码StreamFlag失败！")
			}
			recvPacket.StreamFlag = frame.StreamFlag(streamFlag)

			if recvPacket.StreamNo, err = dec.String(); err != nil {
				return nil, errors.Wrap(err, "解码StreamNo失败！")
			}
			if recvPacket.StreamId, err = dec.Uint64(); err != nil {
				return nil, errors.Wrap(err, "解码StreamId失败！")
			}
		}
	}
	// 消息全局唯一ID
	if recvPacket.MessageID, err = dec.Int64(); err != nil {
		return nil, errors.Wrap(err, "解码MessageId失败！")
	}
	// 消息序列号 （用户唯一，有序递增）
	if recvPacket.MessageSeq, err = decodeMessageSeq(dec, version); err != nil {
		return nil, errors.Wrap(err, "解码MessageSeq失败！")
	}
	// 消息时间
	if recvPacket.Timestamp, err = dec.Int32(); err != nil {
		return nil, errors.Wrap(err, "解码Timestamp失败！")
	}

	if recvPacket.Setting.IsSet(frame.SettingTopic) {
		// topic
		if recvPacket.Topic, err = dec.String(); err != nil {
			return nil, errors.Wrap(err, "解密topic消息失败！")
		}
	}

	// payloadStartLen := 8 + 4 + 4 + uint32(len(recvPacket.ChannelID)+2) + 1 + uint32(len(recvPacket.FromUID)+2) // 消息ID长度 + 消息序列号长度 + 消息时间长度 +频道ID长度+字符串标示长度 + 频道类型长度 + 发送者uid长度
	// if version > 1 {
	// 	payloadStartLen += uint32(len(recvPacket.ClientMsgNo) + 2)
	// }
	// if version > 2 {
	// 	payloadStartLen += uint32(len(recvPacket.MsgKey) + 2)
	// }
	// if version > 3 {
	// 	payloadStartLen += 1 // 设置的长度
	// }
	// if uint32(len(data)) < payloadStartLen {
	// 	return nil, errors.New("解码RECV消息时失败！payload开始长度位置大于整个剩余数据长度！")
	// }
	// recvPacket.Payload = data[payloadStartLen:]
	if recvPacket.Payload, err = dec.BinaryAll(); err != nil {
		return nil, errors.Wrap(err, "解码payload失败！")
	}
	return recvPacket, err
}

func encodeRecv(recvPacket *frame.RecvPacket, enc *Encoder, version uint8) error {
	// setting
	_ = enc.WriteByte(recvPacket.Setting.Uint8())
	// MsgKey
	enc.WriteString(recvPacket.MsgKey)
	// 发送者
	enc.WriteString(recvPacket.FromUID)
	// 频道ID
	enc.WriteString(recvPacket.ChannelID)
	// 频道类型
	enc.WriteUint8(recvPacket.ChannelType)
	if version >= 3 {
		enc.WriteUint32(recvPacket.Expire)
	}
	// 客户端唯一标示
	enc.WriteString(recvPacket.ClientMsgNo)
	// 流消息
	if version < 5 { // 5版本后不再支持recv里不再需要streamNo和streamId
		if version >= 2 && recvPacket.Setting.IsSet(frame.SettingStream) {
			enc.WriteUint8(uint8(recvPacket.StreamFlag))
			enc.WriteString(recvPacket.StreamNo)
			enc.WriteUint64(recvPacket.StreamId)
		}
	}

	// 消息唯一ID
	enc.WriteInt64(recvPacket.MessageID)
	// 消息有序ID
	if err := encodeMessageSeq(enc, version, recvPacket.MessageSeq); err != nil {
		return err
	}
	// 消息时间戳
	enc.WriteInt32(recvPacket.Timestamp)

	if recvPacket.Setting.IsSet(frame.SettingTopic) {
		enc.WriteString(recvPacket.Topic)
	}
	// 消息内容
	enc.WriteBytes(recvPacket.Payload)
	return nil
}

func encodeRecvSize(packet *frame.RecvPacket, version uint8) int {
	size := 0
	size += frame.SettingByteSize
	size += len(packet.MsgKey) + frame.StringFixLenByteSize
	size += len(packet.FromUID) + frame.StringFixLenByteSize
	size += len(packet.ChannelID) + frame.StringFixLenByteSize
	size += frame.ChannelTypeByteSize
	if version >= 3 {
		size += frame.ExpireByteSize
	}
	size += len(packet.ClientMsgNo) + frame.StringFixLenByteSize
	if version < 5 { // 5版本后不再支持recv里不再需要streamNo和streamId
		if version >= 2 && packet.Setting.IsSet(frame.SettingStream) {
			size += frame.StreamFlagByteSize
			size += len(packet.StreamNo) + frame.StringFixLenByteSize
			size += frame.StreamIdByteSize
		}
	}
	size += frame.MessageIDByteSize
	size += messageSeqSize(version)
	size += frame.TimestampByteSize
	if packet.Setting.IsSet(frame.SettingTopic) {
		size += len(packet.Topic) + frame.StringFixLenByteSize
	}
	size += len(packet.Payload)
	return size
}
