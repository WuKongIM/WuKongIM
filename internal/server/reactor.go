package server

import (
	"github.com/WuKongIM/WuKongIM/internal/reactor"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

type reactorUserMessage struct {
	conn  *connContext
	frame wkproto.Frame
	index uint64
}

func (m *reactorUserMessage) Conn() reactor.Conn {
	return m.conn
}

func (m *reactorUserMessage) Frame() wkproto.Frame {
	return m.frame
}

func (m *reactorUserMessage) Size() uint64 {
	return 0
}

func (m *reactorUserMessage) SetIndex(index uint64) {
	m.index = index
}

func (m *reactorUserMessage) Index() uint64 {
	return m.index
}
