package reactor

import wkproto "github.com/WuKongIM/WuKongIMGoProto"

type Message interface {
	Conn() Conn
	Frame() wkproto.Frame
	Size() uint64
}
