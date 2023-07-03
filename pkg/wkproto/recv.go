package wkproto

import (
	"fmt"

	"github.com/pkg/errors"
)

type StreamFlag uint8

const (
	StreamFlagStart StreamFlag = 0 // 开始
	StreamFlagIng   StreamFlag = 1 // 进行中
	StreamFlagEnd   StreamFlag = 2 // 结束
)

// RecvPacket 收到消息的包
type RecvPacket struct {
	Framer
	Setting     Setting
	MsgKey      string     // 用于验证此消息是否合法（仿中间人篡改）
	MessageID   int64      // 服务端的消息ID(全局唯一)
	MessageSeq  uint32     // 消息序列号 （用户唯一，有序递增）
	ClientMsgNo string     // 客户端唯一标示
	StreamNo    string     // 流式编号
	StreamSeq   uint32     // 流式序列号
	StreamFlag  StreamFlag // 流式标记
	Timestamp   int32      // 服务器消息时间戳(10位，到秒)
	ChannelID   string     // 频道ID
	ChannelType uint8      // 频道类型
	Topic       string     // 话题ID
	FromUID     string     // 发送者UID
	Payload     []byte     // 消息内容

	// ---------- 以下不参与编码 ------------
	ClientSeq uint64 // 客户端提供的序列号，在客户端内唯一
}

// GetPacketType 获得包类型
func (r *RecvPacket) GetFrameType() FrameType {
	return RECV
}

// VerityString 验证字符串
func (r *RecvPacket) VerityString() string {
	return fmt.Sprintf("%d%d%s%d%s%s%d%s", r.MessageID, r.MessageSeq, r.ClientMsgNo, r.Timestamp, r.FromUID, r.ChannelID, r.ChannelType, string(r.Payload))
}
func (r *RecvPacket) String() string {
	return fmt.Sprintf("recv Header:%s Setting:%d MessageID:%d MessageSeq:%d Timestamp:%d FromUid:%s ChannelID:%s ChannelType:%d Topic:%s Payload:%s", r.Framer, r.Setting, r.MessageID, r.MessageSeq, r.Timestamp, r.FromUID, r.ChannelID, r.ChannelType, r.Topic, string(r.Payload))
}

func decodeRecv(frame Frame, data []byte, version uint8) (Frame, error) {
	dec := NewDecoder(data)
	recvPacket := &RecvPacket{}
	recvPacket.Framer = frame.(Framer)
	var err error
	setting, err := dec.Uint8()
	if err != nil {
		return nil, errors.Wrap(err, "解码消息设置失败！")
	}
	recvPacket.Setting = Setting(setting)
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
	// 客户端唯一标示
	if recvPacket.ClientMsgNo, err = dec.String(); err != nil {
		return nil, errors.Wrap(err, "解码ClientMsgNo失败！")
	}
	// 流消息
	if version >= 2 && recvPacket.Setting.IsSet(SettingStream) {
		if recvPacket.StreamNo, err = dec.String(); err != nil {
			return nil, errors.Wrap(err, "解码StreamNo失败！")
		}
		if recvPacket.StreamSeq, err = dec.Uint32(); err != nil {
			return nil, errors.Wrap(err, "解码StreamSeq失败！")
		}
		var streamFlag uint8
		if streamFlag, err = dec.Uint8(); err != nil {
			return nil, errors.Wrap(err, "解码StreamFlag失败！")
		}
		recvPacket.StreamFlag = StreamFlag(streamFlag)
	}
	// 消息全局唯一ID
	if recvPacket.MessageID, err = dec.Int64(); err != nil {
		return nil, errors.Wrap(err, "解码MessageId失败！")
	}
	// 消息序列号 （用户唯一，有序递增）
	if recvPacket.MessageSeq, err = dec.Uint32(); err != nil {
		return nil, errors.Wrap(err, "解码MessageSeq失败！")
	}
	// 消息时间
	if recvPacket.Timestamp, err = dec.Int32(); err != nil {
		return nil, errors.Wrap(err, "解码Timestamp失败！")
	}

	if recvPacket.Setting.IsSet(SettingTopic) {
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

func encodeRecv(recvPacket *RecvPacket, enc *Encoder, version uint8) error {
	// setting
	enc.WriteByte(recvPacket.Setting.Uint8())
	// MsgKey
	enc.WriteString(recvPacket.MsgKey)
	// 发送者
	enc.WriteString(recvPacket.FromUID)
	// 频道ID
	enc.WriteString(recvPacket.ChannelID)
	// 频道类型
	enc.WriteUint8(recvPacket.ChannelType)
	// 客户端唯一标示
	enc.WriteString(recvPacket.ClientMsgNo)
	// 流消息
	if version >= 2 && recvPacket.Setting.IsSet(SettingStream) {
		enc.WriteString(recvPacket.StreamNo)
		enc.WriteUint32(recvPacket.StreamSeq)
		enc.WriteUint8(uint8(recvPacket.StreamFlag))
	}
	// 消息唯一ID
	enc.WriteInt64(recvPacket.MessageID)
	// 消息有序ID
	enc.WriteUint32(recvPacket.MessageSeq)
	// 消息时间戳
	enc.WriteInt32(recvPacket.Timestamp)

	if recvPacket.Setting.IsSet(SettingTopic) {
		enc.WriteString(recvPacket.Topic)
	}
	// 消息内容
	enc.WriteBytes(recvPacket.Payload)
	return nil
}

func encodeRecvSize(packet *RecvPacket, version uint8) int {
	size := 0
	size += SettingByteSize

	size += (len(packet.MsgKey) + StringFixLenByteSize)
	size += (len(packet.FromUID) + StringFixLenByteSize)
	size += (len(packet.ChannelID) + StringFixLenByteSize)
	size += ChannelTypeByteSize
	size += (len(packet.ClientMsgNo) + StringFixLenByteSize)
	if version >= 2 && packet.Setting.IsSet(SettingStream) {
		size += (len(packet.StreamNo) + StringFixLenByteSize)
		size += StreamSeqByteSize
		size += StreamFlagByteSize
	}
	size += MessageIDByteSize
	size += MessageSeqByteSize

	size += TimestampByteSize

	if packet.Setting.IsSet(SettingTopic) {
		size += (len(packet.Topic) + StringFixLenByteSize)
	}

	size += len(packet.Payload)

	return size
}
