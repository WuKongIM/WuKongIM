package server

import (
	"bytes"
	"encoding/gob"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkproto"
	"github.com/WuKongIM/WuKongIM/pkg/wkstore"
)

type everyScheduler struct {
	Interval time.Duration
}

func (s *everyScheduler) Next(prev time.Time) time.Time {
	return prev.Add(s.Interval)
}

type Message struct {
	*wkproto.RecvPacket
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

func (m *Message) DeepCopy() (*Message, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(m); err != nil {
		return nil, err
	}
	dst := &Message{}
	err := gob.NewDecoder(bytes.NewBuffer(buf.Bytes())).Decode(dst)
	if err != nil {
		return nil, err
	}
	dst.fromDeviceID = m.fromDeviceID
	dst.fromDeviceFlag = m.fromDeviceFlag
	dst.toClientID = m.toClientID
	dst.large = m.large
	dst.index = m.index
	dst.pri = m.pri
	dst.retryCount = m.retryCount
	return dst, nil
}

// MarshalMessage MarshalMessage
func MarshalMessage(version uint8, m *Message) []byte {
	enc := wkproto.NewEncoder()
	enc.WriteByte(wkproto.ToFixHeaderUint8(m.RecvPacket))
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
	recvPacket := &wkproto.RecvPacket{}
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
