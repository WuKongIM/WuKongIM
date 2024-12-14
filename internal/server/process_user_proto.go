package server

import (
	"github.com/WuKongIM/WuKongIM/internal/reactor"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

type msgType uint32

const (
	// 用户节点加入，用户副本节点加入领导节点时发起
	msgUserJoinReq msgType = 2000
	// 消息发件箱请求
	msgOutboundReq msgType = 2001
)

type userJoinReq struct {
	from uint64
	uid  string
}

func (u *userJoinReq) encode() []byte {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint64(u.from)
	enc.WriteString(u.uid)

	return enc.Bytes()
}

func (u *userJoinReq) decode(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if u.from, err = dec.Uint64(); err != nil {
		return err
	}
	if u.uid, err = dec.String(); err != nil {
		return err
	}
	return nil
}

type outboundReq struct {
	fromNode uint64
	uid      string
	messages reactor.UserMessageBatch
}

func (o *outboundReq) encode() ([]byte, error) {
	enc := wkproto.NewEncoder()
	enc.WriteString(o.uid)
	enc.WriteUint64(o.fromNode)
	if len(o.messages) > 0 {
		msgData, err := o.messages.Encode()
		if err != nil {
			return nil, err
		}
		enc.WriteBytes(msgData)
	}
	return enc.Bytes(), nil
}

func (o *outboundReq) decode(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if o.uid, err = dec.String(); err != nil {
		return err
	}
	if o.fromNode, err = dec.Uint64(); err != nil {
		return err
	}
	msgData, err := dec.BinaryAll()
	if err != nil {
		return err
	}
	return o.messages.Decode(msgData)
}
