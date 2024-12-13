package reactor

import wkproto "github.com/WuKongIM/WuKongIMGoProto"

type UserMessage interface {
	Conn() Conn
	Frame() wkproto.Frame
	Size() uint64
	SetIndex(index uint64)
	Index() uint64
}

type defaultUserMessage struct {
	conn  Conn
	frame wkproto.Frame
	index uint64
}

func (m *defaultUserMessage) Conn() Conn {
	return m.conn
}

func (m *defaultUserMessage) Frame() wkproto.Frame {
	return m.frame
}

func (m *defaultUserMessage) Size() uint64 {
	return 0
}

func (m *defaultUserMessage) SetIndex(index uint64) {
	m.index = index
}

func (m *defaultUserMessage) Index() uint64 {
	return m.index
}
