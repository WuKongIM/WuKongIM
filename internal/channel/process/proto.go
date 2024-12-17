package process

import (
	"github.com/WuKongIM/WuKongIM/internal/reactor"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

type msgType uint32

// 范围 options.ReactorChannelMsgTypeMin ~ options.ReactorChannelMsgTypeMax
const (
	// 用户节点加入，用户副本节点加入领导节点时发起
	msgChannelJoinReq  msgType = 3001
	msgChannelJoinResp msgType = 3002
	// 消息发件箱请求
	msgOutboundReq msgType = 3003
	// 节点心跳 领导节点发起
	msgNodeHeartbeatReq msgType = 3004
	// 节点心跳回执 副本节点回执
	msgNodeHeartbeatResp msgType = 3005
	//	 发送消息回执
	msgSendack msgType = 3006
)

func (m msgType) uint32() uint32 {
	return uint32(m)
}

func (m msgType) String() string {
	switch m {
	case msgChannelJoinReq:
		return "msgChannelJoinReq"
	case msgChannelJoinResp:
		return "msgChannelJoinResp"
	case msgOutboundReq:
		return "msgOutboundReq"
	case msgNodeHeartbeatReq:
		return "msgNodeHeartbeatReq"
	case msgNodeHeartbeatResp:
		return "msgNodeHeartbeatResp"
	case msgSendack:
		return "msgSendack"
	default:
		return "unknown msgType"
	}
}

type allowSendReq struct {
	From string // 发送者
	To   string // 接收者
}

func (a *allowSendReq) decode(data []byte) error {

	dec := wkproto.NewDecoder(data)
	var err error
	if a.From, err = dec.String(); err != nil {
		return err
	}
	if a.To, err = dec.String(); err != nil {
		return err
	}
	return nil
}

func (a *allowSendReq) encode() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteString(a.From)
	enc.WriteString(a.To)
	return enc.Bytes(), nil
}

type sendackReq struct {
	framer       uint8
	protoVersion uint8
	messageId    int64
	messageSeq   uint64
	clientSeq    uint64
	clientMsgNo  string
	reasonCode   uint8
	fromUid      string
	FromNode     uint64
	ConnId       int64
}

func (s *sendackReq) encode() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint8(s.framer)
	enc.WriteUint8(s.protoVersion)
	enc.WriteInt64(s.messageId)
	enc.WriteUint64(s.messageSeq)
	enc.WriteUint64(s.clientSeq)
	enc.WriteString(s.clientMsgNo)
	enc.WriteUint8(s.reasonCode)
	enc.WriteString(s.fromUid)
	enc.WriteUint64(s.FromNode)
	enc.WriteInt64(s.ConnId)
	return enc.Bytes(), nil
}

func (s *sendackReq) decode(data []byte) error {

	dec := wkproto.NewDecoder(data)
	var err error
	if s.framer, err = dec.Uint8(); err != nil {
		return err
	}
	if s.protoVersion, err = dec.Uint8(); err != nil {
		return err
	}
	if s.messageId, err = dec.Int64(); err != nil {
		return err
	}

	if s.messageSeq, err = dec.Uint64(); err != nil {
		return err
	}

	if s.clientSeq, err = dec.Uint64(); err != nil {
		return err
	}
	if s.clientMsgNo, err = dec.String(); err != nil {
		return err
	}
	if s.reasonCode, err = dec.Uint8(); err != nil {
		return err
	}
	if s.fromUid, err = dec.String(); err != nil {
		return err
	}
	if s.FromNode, err = dec.Uint64(); err != nil {
		return err
	}
	if s.ConnId, err = dec.Int64(); err != nil {
		return err
	}
	return nil
}

type sendackBatchReq []*sendackReq

func (s sendackBatchReq) encode() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint16(uint16(len(s)))
	for _, v := range s {
		enc.WriteUint8(v.framer)
		enc.WriteUint8(v.protoVersion)
		enc.WriteInt64(v.messageId)
		enc.WriteUint64(v.messageSeq)
		enc.WriteUint64(v.clientSeq)
		enc.WriteString(v.clientMsgNo)
		enc.WriteUint8(v.reasonCode)
		enc.WriteString(v.fromUid)
		enc.WriteUint64(v.FromNode)
		enc.WriteInt64(v.ConnId)
	}
	return enc.Bytes(), nil
}

func (s *sendackBatchReq) decode(data []byte) error {

	dec := wkproto.NewDecoder(data)
	var err error
	var size uint16
	if size, err = dec.Uint16(); err != nil {
		return err
	}
	for i := 0; i < int(size); i++ {
		var v sendackReq
		if v.framer, err = dec.Uint8(); err != nil {
			return err
		}
		if v.protoVersion, err = dec.Uint8(); err != nil {
			return err
		}
		if v.messageId, err = dec.Int64(); err != nil {
			return err
		}

		if v.messageSeq, err = dec.Uint64(); err != nil {
			return err
		}

		if v.clientSeq, err = dec.Uint64(); err != nil {
			return err
		}
		if v.clientMsgNo, err = dec.String(); err != nil {
			return err
		}
		if v.reasonCode, err = dec.Uint8(); err != nil {
			return err
		}
		if v.fromUid, err = dec.String(); err != nil {
			return err
		}
		if v.FromNode, err = dec.Uint64(); err != nil {
			return err
		}
		if v.ConnId, err = dec.Int64(); err != nil {
			return err
		}
		*s = append(*s, &v)
	}
	return nil
}

type outboundReq struct {
	channelId   string
	channelType uint8
	fromNode    uint64
	messages    reactor.ChannelMessageBatch
}

func (o *outboundReq) encode() ([]byte, error) {
	enc := wkproto.NewEncoder()
	enc.WriteString(o.channelId)
	enc.WriteUint8(o.channelType)
	enc.WriteUint64(o.fromNode)
	data, err := o.messages.Encode()
	if err != nil {
		return nil, err
	}
	enc.WriteBytes(data)
	return enc.Bytes(), nil
}

func (o *outboundReq) decode(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if o.channelId, err = dec.String(); err != nil {
		return err
	}
	if o.channelType, err = dec.Uint8(); err != nil {
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

type channelJoinReq struct {
	channelId   string
	channelType uint8
	from        uint64
}

func (c *channelJoinReq) encode() ([]byte, error) {
	enc := wkproto.NewEncoder()
	enc.WriteString(c.channelId)
	enc.WriteUint8(c.channelType)
	enc.WriteUint64(c.from)
	return enc.Bytes(), nil
}

func (c *channelJoinReq) decode(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if c.channelId, err = dec.String(); err != nil {
		return err
	}
	if c.channelType, err = dec.Uint8(); err != nil {
		return err
	}
	if c.from, err = dec.Uint64(); err != nil {
		return err
	}
	return nil
}

type channelJoinResp struct {
	channelId   string
	channelType uint8
	from        uint64
}

func (c *channelJoinResp) encode() ([]byte, error) {
	enc := wkproto.NewEncoder()
	enc.WriteString(c.channelId)
	enc.WriteUint8(c.channelType)
	enc.WriteUint64(c.from)
	return enc.Bytes(), nil
}

func (c *channelJoinResp) decode(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if c.channelId, err = dec.String(); err != nil {
		return err
	}
	if c.channelType, err = dec.Uint8(); err != nil {
		return err
	}
	if c.from, err = dec.Uint64(); err != nil {
		return err
	}
	return nil
}

type nodeHeartbeatReq struct {
	channelId   string
	channelType uint8
	fromNode    uint64
}

func (n *nodeHeartbeatReq) encode() ([]byte, error) {
	enc := wkproto.NewEncoder()
	enc.WriteString(n.channelId)
	enc.WriteUint8(n.channelType)
	enc.WriteUint64(n.fromNode)
	return enc.Bytes(), nil
}

func (n *nodeHeartbeatReq) decode(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if n.channelId, err = dec.String(); err != nil {
		return err
	}
	if n.channelType, err = dec.Uint8(); err != nil {
		return err
	}
	if n.fromNode, err = dec.Uint64(); err != nil {
		return err
	}
	return nil
}

type nodeHeartbeatResp struct {
	channelId   string
	channelType uint8
	fromNode    uint64
}

func (n *nodeHeartbeatResp) encode() ([]byte, error) {
	enc := wkproto.NewEncoder()
	enc.WriteString(n.channelId)
	enc.WriteUint8(n.channelType)
	enc.WriteUint64(n.fromNode)
	return enc.Bytes(), nil
}

func (n *nodeHeartbeatResp) decode(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if n.channelId, err = dec.String(); err != nil {
		return err
	}
	if n.channelType, err = dec.Uint8(); err != nil {
		return err
	}
	if n.fromNode, err = dec.Uint64(); err != nil {
		return err
	}
	return nil
}
