package server

import (
	"sync"

	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

type FramePool struct {
	sendPacketsPool    sync.Pool
	recvackPacketsPool sync.Pool
	subPacketsPool     sync.Pool
	subackPacketsPool  sync.Pool
}

func NewFramePool() *FramePool {

	return &FramePool{
		sendPacketsPool: sync.Pool{
			New: func() any {
				return make([]*wkproto.SendPacket, 0)
			},
		},
		recvackPacketsPool: sync.Pool{
			New: func() any {
				return make([]*wkproto.RecvackPacket, 0)
			},
		},
		subPacketsPool: sync.Pool{
			New: func() any {
				return make([]*wkproto.SubPacket, 0)
			},
		},
		subackPacketsPool: sync.Pool{
			New: func() any {
				return make([]*wkproto.SubackPacket, 0)
			},
		},
	}
}

func (f *FramePool) GetSendPackets() []*wkproto.SendPacket {
	return f.sendPacketsPool.Get().([]*wkproto.SendPacket)
}

func (f *FramePool) PutSendPackets(sendPackets []*wkproto.SendPacket) {
	s := sendPackets[:0]
	f.sendPacketsPool.Put(s)
}

func (f *FramePool) GetRecvackPackets() []*wkproto.RecvackPacket {
	return f.recvackPacketsPool.Get().([]*wkproto.RecvackPacket)
}
func (f *FramePool) PutRecvackPackets(recvackPackets []*wkproto.RecvackPacket) {
	s := recvackPackets[:0]
	f.recvackPacketsPool.Put(s)
}

func (f *FramePool) GetSubPackets() []*wkproto.SubPacket {
	return f.subPacketsPool.Get().([]*wkproto.SubPacket)
}
func (f *FramePool) PutSubPackets(subPackets []*wkproto.SubPacket) {
	s := subPackets[:0]
	f.subPacketsPool.Put(s)
}
func (f *FramePool) GetSubackPackets() []*wkproto.SubackPacket {
	return f.subackPacketsPool.Get().([]*wkproto.SubackPacket)
}

func (f *FramePool) PutSubackPackets(subackPackets []*wkproto.SubackPacket) {
	s := subackPackets[:0]
	f.subackPacketsPool.Put(s)
}
