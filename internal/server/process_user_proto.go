package server

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/reactor"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

type msgType uint32

const (
	// 用户节点加入，用户副本节点加入领导节点时发起
	msgUserJoinReq  msgType = 2000
	msgUserJoinResp msgType = 2001
	// 消息发件箱请求
	msgOutboundReq msgType = 2002
	// 节点心跳 领导节点发起
	msgNodeHeartbeatReq msgType = 2003
	// 节点心跳回执 副本节点回执
	msgNodeHeartbeatResp msgType = 2004
)

func (m msgType) String() string {
	switch m {
	case msgUserJoinReq:
		return "msgUserJoinReq"
	case msgUserJoinResp:
		return "msgUserJoinResp"
	case msgOutboundReq:
		return "msgOutboundReq"
	case msgNodeHeartbeatReq:
		return "msgNodeHeartbeatReq"
	case msgNodeHeartbeatResp:
		return "msgNodeHeartbeatResp"
	default:
		return fmt.Sprintf("unknown msgType: %d", m)
	}
}

type userJoinReq struct {
	uid  string
	from uint64
}

func (u *userJoinReq) encode() []byte {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteString(u.uid)
	enc.WriteUint64(u.from)

	return enc.Bytes()
}

func (u *userJoinReq) decode(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if u.uid, err = dec.String(); err != nil {
		return err
	}
	if u.from, err = dec.Uint64(); err != nil {
		return err
	}

	return nil
}

type userJoinResp struct {
	from uint64
	uid  string
}

func (u *userJoinResp) encode() []byte {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteString(u.uid)
	enc.WriteUint64(u.from)

	return enc.Bytes()
}

func (u *userJoinResp) decode(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error

	if u.uid, err = dec.String(); err != nil {
		return err
	}
	if u.from, err = dec.Uint64(); err != nil {
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
	if len(msgData) == 0 {
		return nil
	}
	return o.messages.Decode(msgData)
}

type nodeHeartbeatReq struct {
	uid      string
	fromNode uint64
	connIds  []int64
}

func (n *nodeHeartbeatReq) encode() []byte {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteString(n.uid)
	enc.WriteUint64(n.fromNode)
	enc.WriteUint16(uint16(len(n.connIds)))
	for _, id := range n.connIds {
		enc.WriteUint64(uint64(id))
	}
	return enc.Bytes()
}

func (n *nodeHeartbeatReq) decode(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error

	if n.uid, err = dec.String(); err != nil {
		return err
	}

	if n.fromNode, err = dec.Uint64(); err != nil {
		return err
	}
	numConnIds, err := dec.Uint16()
	if err != nil {
		return err
	}
	n.connIds = make([]int64, numConnIds)
	for i := uint16(0); i < numConnIds; i++ {
		if n.connIds[i], err = dec.Int64(); err != nil {
			return err
		}
	}
	return nil
}

type nodeHeartbeatResp struct {
	uid      string
	fromNode uint64
	connIds  []int64
}

func (n *nodeHeartbeatResp) encode() []byte {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteString(n.uid)
	enc.WriteUint64(n.fromNode)
	enc.WriteUint16(uint16(len(n.connIds)))
	for _, id := range n.connIds {
		enc.WriteUint64(uint64(id))
	}
	return enc.Bytes()
}

func (n *nodeHeartbeatResp) decode(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if n.uid, err = dec.String(); err != nil {
		return err
	}
	if n.fromNode, err = dec.Uint64(); err != nil {
		return err
	}
	numConnIds, err := dec.Uint16()
	if err != nil {
		return err
	}
	n.connIds = make([]int64, numConnIds)
	for i := uint16(0); i < numConnIds; i++ {
		if n.connIds[i], err = dec.Int64(); err != nil {
			return err
		}
	}
	return nil
}
