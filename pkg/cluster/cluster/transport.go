package cluster

import (
	replica "github.com/WuKongIM/WuKongIM/pkg/cluster/replica2"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

type ITransport interface {
	// Send 发送消息
	Send(m Message) error
	// OnMessage 收取消息
	OnMessage(f func(m Message))
}

type Message struct {
	ShardNo string
	replica.Message
}

func NewMessage(shardNo string, msg replica.Message) Message {
	return Message{
		ShardNo: shardNo,
		Message: msg,
	}
}

func (m Message) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()

	enc.WriteString(m.ShardNo)
	msgData, err := m.Message.Marshal()
	if err != nil {
		return nil, err
	}
	enc.WriteBytes(msgData)
	return enc.Bytes(), nil
}

func UnmarshalMessage(data []byte) (Message, error) {
	m := Message{}
	dec := wkproto.NewDecoder(data)
	var err error
	if m.ShardNo, err = dec.String(); err != nil {
		return m, err
	}
	msgData, err := dec.BinaryAll()
	if err != nil {
		return m, err
	}
	m.Message, err = replica.UnmarshalMessage(msgData)
	if err != nil {
		return m, err
	}
	return m, nil
}

type MemoryTransport struct {
	nodeMessageListenerMap map[uint64]func(m Message)
}

func NewMemoryTransport() *MemoryTransport {
	return &MemoryTransport{
		nodeMessageListenerMap: make(map[uint64]func(m Message)),
	}
}

func (t *MemoryTransport) Send(m Message) error {
	if f, ok := t.nodeMessageListenerMap[m.To]; ok {
		go f(m) // 模拟网络请求
	}
	return nil
}

func (t *MemoryTransport) OnMessage(f func(m Message)) {

}

func (t *MemoryTransport) OnNodeMessage(nodeID uint64, f func(m Message)) {
	t.nodeMessageListenerMap[nodeID] = f
}
