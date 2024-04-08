package cluster

import (
	"encoding/binary"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/trace"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
)

type ITransport interface {
	// Send 发送消息
	Send(to uint64, m *proto.Message, callback func()) error
	// OnMessage 收取消息
	// OnMessage(f func(from uint64, m *proto.Message))
	// 收到消息
	// RecvMessage(from uint64, m *proto.Message)
}

func NewMessage(shardNo string, msg replica.Message, msgType proto.MsgType) (*proto.Message, error) {
	m := Message{
		ShardNo: shardNo,
		Message: msg,
	}
	data, err := m.Marshal()
	if err != nil {
		return nil, err
	}
	return &proto.Message{
		MsgType: uint32(msgType),
		Content: data,
	}, nil

}

func NewMessageFromProto(m *proto.Message) (Message, error) {
	return UnmarshalMessage(m.Content)
}

type Message struct {
	ShardNo string
	replica.Message
}

func (m Message) Marshal() ([]byte, error) {

	msgBytes, err := m.Message.Marshal()
	if err != nil {
		return nil, err
	}
	resultBytes := make([]byte, 2+len(m.ShardNo)+len(msgBytes))
	binary.BigEndian.PutUint16(resultBytes, uint16(len(m.ShardNo)))
	copy(resultBytes[2:], []byte(m.ShardNo))
	copy(resultBytes[2+len(m.ShardNo):], msgBytes)
	return resultBytes, nil
}

func UnmarshalMessage(data []byte) (Message, error) {
	shardNoLen := binary.BigEndian.Uint16(data)
	shardNo := string(data[2 : 2+shardNoLen])
	msg, err := replica.UnmarshalMessage(data[2+shardNoLen:])
	if err != nil {
		return Message{}, err
	}
	return Message{
		ShardNo: shardNo,
		Message: msg,
	}, nil
}

type MemoryTransport struct {
	nodeMessageListenerMap map[uint64]func(m *proto.Message)
}

func NewMemoryTransport() *MemoryTransport {
	return &MemoryTransport{
		nodeMessageListenerMap: make(map[uint64]func(m *proto.Message)),
	}
}

func (t *MemoryTransport) Send(to uint64, m *proto.Message, callback func()) error {
	if f, ok := t.nodeMessageListenerMap[to]; ok {
		go func() {
			f(m) // 模拟网络请求
			callback()
		}()
	}
	return nil
}

func (t *MemoryTransport) OnMessage(f func(from uint64, m *proto.Message)) {

}

func (t *MemoryTransport) RecvMessage(from uint64, m *proto.Message) {
}

func (t *MemoryTransport) OnNodeMessage(nodeID uint64, f func(m *proto.Message)) {
	t.nodeMessageListenerMap[nodeID] = f
}

type DefaultTransport struct {
	s         *Server
	onMessage func(from uint64, m *proto.Message)
}

func NewDefaultTransport(s *Server) *DefaultTransport {
	d := &DefaultTransport{
		s: s,
	}

	return d
}

func (d *DefaultTransport) Send(to uint64, m *proto.Message, callback func()) error {
	trace.GlobalTrace.Metrics.Cluster().MessageOutgoingCountAdd(1)
	trace.GlobalTrace.Metrics.Cluster().MessageOutgoingBytesAdd(int64(m.Size()))
	node := d.s.nodeManager.node(to)
	if node == nil {
		return ErrNodeNotFound
	}
	return node.send(m)
}

func (d *DefaultTransport) OnMessage(f func(from uint64, m *proto.Message)) {
	d.onMessage = f
}

func (d *DefaultTransport) RecvMessage(from uint64, m *proto.Message) {

	if d.onMessage != nil {
		d.onMessage(from, m)
	}
}
