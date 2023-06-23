package server

import (
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/wkproto"
)

type FramePool struct {
	sendPacketsPool    sync.Pool
	recvackPacketsPool sync.Pool
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
