package reactor

import (
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

// type UserMessage interface {
// 	GetConn() *Conn
// 	GetFrame() wkproto.Frame
// 	Size() uint64
// 	// 消息顺序下标
// 	SetIndex(index uint64)
// 	GetIndex() uint64
// 	// ToNode 此条消息只发给此节点
// 	GetToNode() uint64
// 	SetToNode(to uint64)

// 	// GetWriteData 获取写入数据
// 	GetWriteData() []byte
// 	SetWriteData(data []byte)
// }

type UserMessage struct {
	Conn      *Conn
	Frame     wkproto.Frame
	WriteData []byte
	Index     uint64
	ToNode    uint64
	MessageId int64 // 消息id

}

func (m *UserMessage) Size() uint64 {
	size := uint64(0)
	if m.Conn != nil {
		size += m.Conn.Size()
	}
	if m.Frame != nil {
		size += uint64(m.Frame.GetFrameSize())
	}
	size += uint64(len(m.WriteData))
	size += 8 + 8
	return size
}

func (m *UserMessage) Encode() ([]byte, error) {
	// flag
	// conn frame writeData 0 0 0 0
	hasConn := m.hasConn()
	hasFrame := m.hasFrame()
	hasWriteData := m.hasWriteData()

	var flag uint8 = hasConn<<7 | hasFrame<<6 | hasWriteData<<5

	enc := wkproto.NewEncoder()
	defer enc.End()

	enc.WriteUint8(flag)
	if hasConn == 1 {
		data, err := m.Conn.Encode()
		if err != nil {
			return nil, err
		}
		enc.WriteBinary(data)
	}
	if hasFrame == 1 {
		data, err := Proto.EncodeFrame(m.Frame, wkproto.LatestVersion)
		if err != nil {
			return nil, err
		}
		enc.WriteUint32(uint32(len(data)))
		enc.WriteBytes(data)
	}
	if hasWriteData == 1 {
		enc.WriteUint32(uint32(len(m.WriteData)))
		enc.WriteBytes(m.WriteData)
	}

	enc.WriteUint64(m.Index)
	enc.WriteUint64(m.ToNode)
	enc.WriteInt64(m.MessageId)

	return enc.Bytes(), nil
}

func (m *UserMessage) Decode(data []byte) error {

	dec := wkproto.NewDecoder(data)
	flag, err := dec.Uint8()
	if err != nil {
		return err
	}
	hasConn := flag >> 7 & 0x01
	hasFrame := flag >> 6 & 0x01
	hasWriteData := flag >> 5 & 0x01

	if hasConn == 1 {
		connBytes, err := dec.Binary()
		if err != nil {
			return err
		}
		conn := &Conn{}
		err = conn.Decode(connBytes)
		if err != nil {
			return err
		}
		m.Conn = conn
	}
	if hasFrame == 1 {
		frameLen, err := dec.Uint32()
		if err != nil {
			return err
		}
		data, err := dec.Bytes(int(frameLen))
		if err != nil {
			return err
		}
		frame, _, err := Proto.DecodeFrame(data, wkproto.LatestVersion)
		if err != nil {
			return err
		}
		m.Frame = frame
	}

	if hasWriteData == 1 {
		writeDataLen, err := dec.Uint32()
		if err != nil {
			return err
		}
		data, err := dec.Bytes(int(writeDataLen))
		if err != nil {
			return err
		}
		m.WriteData = data
	}
	if m.Index, err = dec.Uint64(); err != nil {
		return err
	}

	if m.ToNode, err = dec.Uint64(); err != nil {
		return err
	}

	if m.MessageId, err = dec.Int64(); err != nil {
		return err
	}

	return nil

}

func (m *UserMessage) hasConn() uint8 {
	if m.Conn != nil {
		return 1
	}
	return 0
}
func (m *UserMessage) hasFrame() uint8 {
	if m.Frame != nil {
		return 1
	}
	return 0
}

func (m *UserMessage) hasWriteData() uint8 {
	if len(m.WriteData) > 0 {
		return 1
	}
	return 0
}

type UserMessageBatch []*UserMessage

func (u UserMessageBatch) Encode() ([]byte, error) {
	// 创建一个新的编码器
	enc := wkproto.NewEncoder()
	defer enc.End()

	// 编码 UserMessage 的数量
	enc.WriteUint16(uint16(len(u)))

	// 编码每个 UserMessage
	for _, msg := range u {
		// 确保每个消息都有正确的编码
		encodedMsg, err := msg.Encode() // 解引用 msg 获取 UserMessage
		if err != nil {
			return nil, err
		}
		enc.WriteUint32(uint32(len(encodedMsg)))
		enc.WriteBytes(encodedMsg)
	}

	// 返回最终编码的字节数据
	return enc.Bytes(), nil
}

func (u *UserMessageBatch) Decode(data []byte) error {
	// 创建解码器
	dec := wkproto.NewDecoder(data)

	// 解码 UserMessage 的数量
	numMessages, err := dec.Uint16()
	if err != nil {
		return err
	}

	// 解码每个 UserMessage
	*u = make(UserMessageBatch, numMessages)
	for i := uint16(0); i < numMessages; i++ {
		// 解码每个 UserMessage 的字节数据
		dataLen, err := dec.Uint32()
		if err != nil {
			return err
		}

		msgData, err := dec.Bytes(int(dataLen))
		if err != nil {
			return err
		}
		// 创建一个新的 UserMessage
		msg := &UserMessage{}
		// 使用解码数据填充 UserMessage
		err = msg.Decode(msgData)
		if err != nil {
			return err
		}
		// 将解码后的消息添加到 UserMessageBatch 中
		(*u)[i] = msg
	}
	return nil

}
